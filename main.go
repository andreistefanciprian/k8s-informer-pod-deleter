package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	// Kubernetes API types
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	// Kubernetes client libraries
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

// Struct to represent a Pod to be deleted
type PodToDelete struct {
	Namespace string // namespace of the Pod
	Name      string // name of the Pod
}

var (
	eventMessage   string                   // message used to identify events to be processed
	eventReason    string                   // reason used to identify events to be processed
	dryRunMode     bool                     // if true, do not actually delete the Pods
	ctx            = context.TODO()         // context used to make Kubernetes API calls
	podDeleteQueue = make(chan PodToDelete) // channel to hold Pods to be deleted
)

func main() {
	// Define and parse command-line flags
	flag.BoolVar(&dryRunMode, "dry-run", false, "enable dry run mode (no changes are made, only logged)")
	flag.StringVar(&eventReason, "event-reason", "FailedCreatePodSandBox", "specify Event Reason to match")
	flag.StringVar(
		&eventMessage,
		"event-message",
		"container veth name provided (eth0) already exists",
		"specify Event Message to match",
	)

	// Get the path to the kubeconfig file
	defaultKubeconfig := os.Getenv(clientcmd.RecommendedConfigPathEnvVar)
	if len(defaultKubeconfig) == 0 {
		defaultKubeconfig = clientcmd.RecommendedHomeFile
	}

	// Parse the kubeconfig file path from the command-line
	kubeconfig := flag.String(clientcmd.RecommendedConfigPathFlag,
		defaultKubeconfig, "absolute path to the kubeconfig file")
	flag.Parse()

	// Create a Kubernetes client config from the kubeconfig file
	rc, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	// Create a client set from the config
	clientSet, err := kubernetes.NewForConfig(rc)
	if err != nil {
		panic(err.Error())
	}

	// Create a new shared informer factory for all namespaces
	informerFactory := informers.NewSharedInformerFactory(clientSet, time.Minute*1)

	// Use the factory to create an informer for Kubernetes events
	k8sObjectInformer := informerFactory.Core().V1().Events()

	// Add an event handler to the shared informer
	k8sObjectInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    onAdd,
		UpdateFunc: onUpdate,
		DeleteFunc: nil,
	})

	// Start a goroutine to handle the deletion of Pods from the deletion queue
	go func() {
		for {
			select {
			case pd := <-podDeleteQueue:
				if !dryRunMode {
					// Delete the Pod from the Kubernetes API
					err := clientSet.CoreV1().Pods(pd.Namespace).Delete(ctx, pd.Name, metav1.DeleteOptions{})
					if err != nil {
						log.Printf("Failed to delete Pod %s/%s: %v\n", pd.Namespace, pd.Name, err)
					} else {
						log.Printf("Deleted Pod %s/%s\n", pd.Namespace, pd.Name)
					}
				} else {
					log.Printf("[DRY-RUN] Would have deleted Pod %s/%s\n", pd.Namespace, pd.Name)
				}
			}
		}
	}()

	stopCh := make(chan struct{})
	defer close(stopCh)

	// Start the shared informers that have been created by the factory
	informerFactory.Start(stopCh)

	// Wait for the initial synchronization of the local cache
	if !cache.WaitForCacheSync(stopCh, k8sObjectInformer.Informer().HasSynced) {
		panic("failed to sync")
	}

	// Causes the goroutine to block (hit CTRL+C to exit)
	select {}
}

// Handler function to process new events
func onAdd(newObj interface{}) {
	event := newObj.(*corev1.Event)
	processEvent(event, "NEW EVENT")
}

// Handler function to process updated events
func onUpdate(old, new interface{}) {
	// oldEvent := old.(*corev1.Event)
	event := new.(*corev1.Event)
	processEvent(event, "UPDATED EVENT")
}

func processEvent(event *corev1.Event, eventType string) {
	// Check if the event reason and message match the configured values
	if event.Reason == eventReason && strings.Contains(event.Message, eventMessage) {
		podName := event.GetName()
		podNamespace := event.GetNamespace()
		eventResourceVersion := event.GetResourceVersion()
		log.Printf(
			"%s ResVer(%s): Sending Pod %s/%s to the podDeleteQueue.\n",
			eventType,
			eventResourceVersion,
			podNamespace,
			podName,
		)
		// Put the Pod in the deletion queue
		podDeleteQueue <- PodToDelete{
			Namespace: podNamespace,
			Name:      strings.Split(podName, ".")[0],
		}
	}
}
