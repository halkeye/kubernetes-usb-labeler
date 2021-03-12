package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

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
	log.Info(fmt.Sprintf("Namespace: %s | Name: %s | NamespacedName: %s", request.Namespace, request.Name, request.NamespacedName))
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

func generateLabels() map[string]string {
	entryLog := log.WithName("entrypoint")
	results := make(map[string]string)

	// Only one context should be needed for an application.  It should always be closed.
	ctx := gousb.NewContext()
	defer ctx.Close()

	// OpenDevices is used to find the devices to open.
	devs, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		results[fmt.Sprintf("g4v.dev/usb.%s.%s", desc.Vendor.String(), desc.Product.String())] = "true"
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
	entryLog.Info(fmt.Sprintf("generateLabels: %s", results))
	return results
}

var gitDescribe string

func setInterval(someFunc func(), interval time.Duration, async bool) chan bool {
	// Setup the ticket and the channel to signal
	// the ending of the interval
	ticker := time.NewTicker(interval)
	clear := make(chan bool)

	// Put the selection in a go routine
	// so that the for loop is none blocking
	go func() {
		for {

			select {
			case <-ticker.C:
				if async {
					// This won't block
					go someFunc()
				} else {
					// This will block
					someFunc()
				}
			case <-clear:
				ticker.Stop()
				return
			}

		}
	}()

	// We return the channel so we can pass in
	// a value to it to clear the interval
	return clear

}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "USB Node Labeller for Kubernetes\n")
		fmt.Fprintf(os.Stderr, "%s version %s\n", os.Args[0], gitDescribe)
		fmt.Fprintln(os.Stderr, "Usage:")
		flag.PrintDefaults()
	}
	dryRun := flag.Bool("dry-run", false, "Just output labels")
	development := flag.Bool("development", false, "Just output labels")

	flag.Parse()

	if *dryRun == true {
		for k, v := range generateLabels() {
			fmt.Printf("%s = %s\n", k, v)
		}
		os.Exit(0)
	}

	logf.SetLogger(zap.Logger(*development))
	entryLog := log.WithName("entrypoint")

	// Setup a Manager
	entryLog.Info("setting up manager")
	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		MetricsBindAddress: ":3000",
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
	nodeName, err := getNodeName()
	if err != nil {
		entryLog.Error(err, "unable to get the node's name")
		os.Exit(1)
	}

	pred := predicate.Funcs{
		// Create returns true if the Create event should be processed
		CreateFunc: func(e event.CreateEvent) bool {
			// entryLog.Info(fmt.Sprintf("predicate CreateFunc triggered: %s -- %s", e.Meta.GetName(), e.Meta.GetNamespace()))

			if nodeName == e.Meta.GetName() {
				return true
			}
			return false
		},

		// Delete returns true if the Delete event should be processed
		DeleteFunc: func(e event.DeleteEvent) bool {
			// entryLog.Info(fmt.Sprintf("predicate DeleteFunc triggered: %s -- %s", e.Meta.GetName(), e.Meta.GetNamespace()))
			return false
		},

		// Update returns true if the Update event should be processed
		UpdateFunc: func(e event.UpdateEvent) bool {
			// entryLog.Info(fmt.Sprintf("predicate UpdateFunc triggered: %s -- %s", e.MetaNew.GetName(), e.MetaNew.GetNamespace()))
			return false
		},

		// Generic returns true if the Generic event should be processed
		GenericFunc: func(e event.GenericEvent) bool {
			// entryLog.Info("predicate GenericFunc triggered: ")
			return false
		},
	}

	// Watch Nodes and enqueue Nodes object key
	if err := c.Watch(&source.Kind{Type: &corev1.Node{}}, &handler.EnqueueRequestForObject{}, &pred); err != nil {
		entryLog.Error(err, "unable to watch Node")
		os.Exit(1)
	}

	entryLog.Info("starting interval")
	/* Until such time as https://github.com/google/gousb/issues/8 gets merged, just poll every minute or so */
	setInterval(func() {
		req := &reconcile.Request{
			NamespacedName: types.NamespacedName{Name: nodeName, Namespace: ""},
		}
		c.Reconcile(*req)
	}, 1*time.Minute, false)

	entryLog.Info("starting manager")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		entryLog.Error(err, "unable to run manager")
		os.Exit(1)
	}
}

func getNodeName() (string, error) {
	// Within the Kubernetes Pod, the hostname provides the Pod name, rather than the node name, so we pass in the
	// node name via the NODE_NAME environment variable instead.
	nodeName := os.Getenv("NODE_NAME")
	if len(nodeName) > 0 {
		return nodeName, nil
	}

	// If the NODE_NAME environment variable is unset, fall back on hostname matching (e.g. when running outside of
	// a Kubernetes deployment).
	return os.Hostname()
}
