# usb-labeler-controller

Quick hack to add labels for each usb device on a node, to help with using affinity to pin nodes to nodes with devices

I don't know enough about usb identifiers nor kubernetes nodes to know if my current implementation is a good plan, but it does work for me

## Installation

```
# This is a configuration example for the node labeller which includes
# ClusterRole and ClusterRoleBinding definitions, as well as the 
# DeamonSet configuration that deploys the actual node labeller
apiVersion: rbac.authorization.k8s.io/v1beta1
kind: ClusterRole
metadata:
  name: usb-labeler
rules:
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["watch", "get", "list", "update"]
---
apiVersion: rbac.authorization.k8s.io/v1beta1
kind: ClusterRoleBinding
metadata:
  name: labeller
  namespace: default
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: usb-labeler
subjects:
- kind: ServiceAccount
  name: default
  namespace: default
- kind: ServiceAccount
  name: default
  namespace: kube-system
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: usb-labeler
  namespace: kube-system
spec:
  selector:
    matchLabels:
      name: usb-labeler
  template:
    metadata:
      annotations:
        scheduler.alpha.kubernetes.io/critical-pod: ""
      labels:
        name: usb-labeler
    spec:
      tolerations:
      - key: CriticalAddonsOnly
        operator: Exists
      containers:
      - image: halkeye/usb-labeler-controller:latest
        name: usb-labeler
        securityContext:
          privileged: true #Needed for /dev
          capabilities:
            drop: ["ALL"]
        resources:
          requests:
            memory: "10Mi"
            cpu: "1m"
          limits:
            memory: "128Mi"
            cpu: "500m"
        volumeMounts:
          - name: sys
            mountPath: /sys
          - name: dev
            mountPath: /dev
          - name: etc
            mountPath: /etc
      volumes:
        - name: sys
          hostPath:
            path: /sys
        - name: dev
          hostPath:
            path: /dev
        - name: etc
          hostPath:
            path: /etc
```

## Upgrades

* try out https://godoc.org/github.com/chipaca/snappy/interfaces/hotplug to do it with hotplug instead of polling

