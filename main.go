package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"github.com/google/gousb"
)

var log = logf.Log.WithName("usb-node-labeller")

type reconcileNodeLabels struct {
	client client.Client
	log    logr.Logger
	labels map[string]string
}

// make sure reconcileNodeLabels implement the Reconciler interface
var _ reconcile.Reconciler = &reconcileNodeLabels{}

func (r *reconcileNodeLabels) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// set up a convinient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", request)

	node := &corev1.Node{}
	err := r.client.Get(context.TODO(), request.NamespacedName, node)
	if errors.IsNotFound(err) {
		log.Error(nil, "Could not find Node")
		return reconcile.Result{}, nil
	}

	if err != nil {
		log.Error(err, "Could not fetch Node")
		return reconcile.Result{}, err
	}

	// Set the label
	if node.Labels == nil {
		node.Labels = map[string]string{}
	}

	// Remove old labels
	for k := range node.Labels {
		if strings.HasPrefix(k, "g4v.dev") {
			delete(node.Labels, k)
		}
	}

	for k, v := range r.labels {
		node.Labels[k] = v
	}

	err = r.client.Update(context.TODO(), node)
	if err != nil {
		log.Error(err, "Could not write Node")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// Bus 001 Device 005: ID 046d:082d Logitech, Inc. HD Pro Webcam C920
type deviceInfo struct {
	ID      string
	Bus     int
	Label   string
	Vendor  string
	Address int
	Product string
}

func generateLabels() map[string]string {
	entryLog := log.WithName("entrypoint")
	results := make(map[string]string)

	// Only one context should be needed for an application.  It should always be closed.
	ctx := gousb.NewContext()
	defer ctx.Close()

	// OpenDevices is used to find the devices to open.
	devs, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		// The usbid package can be used to print out human readable information.
		// fmt.Printf("%03d.%03d %s:%s %s\n", desc.Bus, desc.Address, desc.Vendor, desc.Product, usbid.Describe(desc))
		// After inspecting the descriptor, return true or false depending on whether
		// the device is "interesting" or not.  Any descriptor for which true is returned
		// opens a Device which is retuned in a slice (and must be subsequently closed).
		// foo := &deviceInfo{
		// 	ID:      fmt.Sprintf("%d:%d", desc.Device.Major(), desc.Device.Minor()),
		// 	Bus:     desc.Bus,
		// 	Label:   usbid.Describe(desc),
		// 	Address: desc.Address,
		// 	Product: desc.Product.String(),
		// 	Vendor:  desc.Vendor.String(),
		// }
		// json, err := json.MarshalIndent(foo, "", "  ")
		// if err != nil {
		// 	entryLog.Error(err, "Error trying to marshal")
		// }
		// fmt.Println(string(json))
		results[fmt.Sprintf("g4v.dev/usb.%s.%s", desc.Product.String(), desc.Vendor.String())] = "true"

		return false
	})

	// All Devices returned from OpenDevices must be closed.
	defer func() {
		for _, d := range devs {
			d.Close()
		}
	}()

	// OpenDevices can occasionally fail, so be sure to check its return value.
	if err != nil {
		entryLog.Error(err, "error listing usb devices")
		os.Exit(1)
	}
	return results
}

var gitDescribe string

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "USB Node Labeller for Kubernetes\n")
		fmt.Fprintf(os.Stderr, "%s version %s\n", os.Args[0], gitDescribe)
		fmt.Fprintln(os.Stderr, "Usage:")
		flag.PrintDefaults()
	}
	dryRun := flag.Bool("dry-run", false, "Just output labels")

	flag.Parse()

	if *dryRun == true {
		for k, v := range generateLabels() {
			fmt.Printf("%s = %s\n", k, v)
		}
		os.Exit(0)
	}

	logf.SetLogger(zap.Logger(false))
	entryLog := log.WithName("entrypoint")

	// Setup a Manager
	entryLog.Info("setting up manager")
	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		MetricsBindAddress: "0",
	})
	if err != nil {
		entryLog.Error(err, "unable to set up overall controller manager")
		os.Exit(1)
	}

	// Setup a new controller to Reconciler Node labels
	entryLog.Info("Setting up controller")
	c, err := controller.New("usb-node-labeller", mgr, controller.Options{
		Reconciler: &reconcileNodeLabels{client: mgr.GetClient(),
			log:    log.WithName("reconciler"),
			labels: generateLabels()},
	})
	if err != nil {
		entryLog.Error(err, "unable to set up individual controller")
		os.Exit(1)
	}

	// laballer only respond to event about the node it is on by matching hostname
	b, err := ioutil.ReadFile("/labeller/hostname")
	if err != nil {
		entryLog.Error(err, "Cannot read hostname")
	}
	hostname := strings.TrimSpace(string(b))

	pred := predicate.Funcs{
		// Create returns true if the Create event should be processed
		CreateFunc: func(e event.CreateEvent) bool {
			if hostname == e.Meta.GetName() {
				return true
			}
			return false
		},

		// Delete returns true if the Delete event should be processed
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},

		// Update returns true if the Update event should be processed
		UpdateFunc: func(e event.UpdateEvent) bool {
			return false
		},

		// Generic returns true if the Generic event should be processed
		GenericFunc: func(e event.GenericEvent) bool {
			//entryLog.Info("predicate generic triggered: ")
			return false
		},
	}

	// Watch Nodes and enqueue Nodes object key
	if err := c.Watch(&source.Kind{Type: &corev1.Node{}}, &handler.EnqueueRequestForObject{}, &pred); err != nil {
		entryLog.Error(err, "unable to watch Node")
		os.Exit(1)
	}

	entryLog.Info("starting manager")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		entryLog.Error(err, "unable to run manager")
		os.Exit(1)
	}
}
