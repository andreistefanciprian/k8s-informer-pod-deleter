package main

import (
	"flag"
	"log"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	types "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

// PodEvent holds events data associated with a Pod
type PodEvent struct {
	UID             types.UID
	PodName         string
	PodNamespace    string
	ResourceVersion string
	EventType       string
	Reason          string
	Message         string
	FirstTimestamp  time.Time
	LastTimestamp   time.Time
}

var (
	eventMessage string
	eventReason  string
	dryRunMode   bool
	// healTime        time.Duration = 5 // allow Pending Pod time to self heal (seconds)
)

func main() {
	// define and parse cli params
	flag.BoolVar(&dryRunMode, "dry-run", false, "enable dry run mode (no changes are made, only logged)")
	flag.StringVar(&eventReason, "reason", "FailedCreatePodSandBox", "restart Pods that match Event Reason")
	flag.StringVar(
		&eventMessage,
		"error-message",
		"container veth name provided (eth0) already exists",
		"number of seconds between iterations",
	)
	defaultKubeconfig := os.Getenv(clientcmd.RecommendedConfigPathEnvVar)
	if len(defaultKubeconfig) == 0 {
		defaultKubeconfig = clientcmd.RecommendedHomeFile
	}

	kubeconfig := flag.String(clientcmd.RecommendedConfigPathFlag,
		defaultKubeconfig, "absolute path to the kubeconfig file")
	flag.Parse()

	rc, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	// create a client set from config
	clientSet, err := kubernetes.NewForConfig(rc)
	if err != nil {
		panic(err.Error())
	}

	// create a new instance of sharedInformerFactory for all namespaces
	informerFactory := informers.NewSharedInformerFactory(clientSet, time.Minute*5)
	// informerFactory := informers.NewFilteredSharedInformerFactory(clientSet, time.Minute*1, "test11", )

	// using this factory create an informer for k8s resources
	k8sObjectInformer := informerFactory.Core().V1().Events()

	// adds an event handler to the shared informer
	k8sObjectInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			item := obj.(*corev1.Event)
			if item.Reason == eventReason && strings.Contains(item.Message, eventMessage) {
				e := PodEvent{
					UID:             item.GetUID(),
					PodName:         item.GetName(),
					PodNamespace:    item.GetNamespace(),
					ResourceVersion: item.GetResourceVersion(),
					EventType:       item.Type,
					Reason:          item.Reason,
					Message:         item.Message,
					FirstTimestamp:  item.FirstTimestamp.Time,
					LastTimestamp:   item.LastTimestamp.Time,
				}
				log.Printf(
					"A: Would have deleted Pod %s/%s\n%+v",
					item.GetNamespace(), item.GetName(), e,
				)
			}
		},

		UpdateFunc: func(old, new interface{}) {
			item := old.(*corev1.Event)
			if item.Reason == eventReason && strings.Contains(item.Message, eventMessage) {
				e := PodEvent{
					UID:             item.GetUID(),
					PodName:         item.GetName(),
					PodNamespace:    item.GetNamespace(),
					ResourceVersion: item.GetResourceVersion(),
					EventType:       item.Type,
					Reason:          item.Reason,
					Message:         item.Message,
					FirstTimestamp:  item.FirstTimestamp.Time,
					LastTimestamp:   item.LastTimestamp.Time,
				}
				log.Printf(
					"U: Would have deleted Pod %s/%s\n%+v",
					item.GetNamespace(), item.GetName(), e,
				)
			}
		},

		DeleteFunc: func(obj interface{}) {
			item := obj.(*corev1.Event)
			if item.Reason == eventReason && strings.Contains(item.Message, eventMessage) {
				e := PodEvent{
					UID:             item.GetUID(),
					PodName:         item.GetName(),
					PodNamespace:    item.GetNamespace(),
					ResourceVersion: item.GetResourceVersion(),
					EventType:       item.Type,
					Reason:          item.Reason,
					Message:         item.Message,
					FirstTimestamp:  item.FirstTimestamp.Time,
					LastTimestamp:   item.LastTimestamp.Time,
				}
				log.Printf(
					"D: Would have deleted Pod %s/%s\n%+v",
					item.GetNamespace(), item.GetName(), e,
				)
			}
		},
	})

	stopCh := make(chan struct{})
	defer close(stopCh)

	// starts the shared informers that have been created by the factory
	informerFactory.Start(stopCh)

	// wait for the initial synchronization of the local cache
	if !cache.WaitForCacheSync(stopCh, k8sObjectInformer.Informer().HasSynced) {
		panic("failed to sync")
	}

	// causes the goroutine to block (hit CTRL+C to exit)
	select {}
}
