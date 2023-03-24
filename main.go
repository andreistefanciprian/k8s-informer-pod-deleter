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
	"k8s.io/client-go/util/workqueue"
)

// Struct to represent a Pod to be deleted
type PodToDelete struct {
	Namespace string // namespace of the Pod
	Name      string // name of the Pod
}

var (
	eventMessage   string                          // message used to identify events to be processed
	eventReason    string                          // reason used to identify events to be processed
	dryRunMode     bool                            // if true, do not actually delete the Pods
	ctx            = context.TODO()                // context used to make Kubernetes API calls
	podDeleteQueue workqueue.RateLimitingInterface // workqueue to hold Pods to be deleted
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

	// Start the worker to handle the deletion of Pods from the work queue
	stopCh := make(chan struct{})
	defer close(stopCh)

	startPodDeletionWorker(clientSet, stopCh)

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
		// Put the Pod in the deletion queue
		podDeleteQueue.Add(PodToDelete{
			Namespace: event.GetNamespace(),
			Name:      event.GetName(),
		})
	}
}

func startPodDeletionWorker(clientSet *kubernetes.Clientset, stopCh <-chan struct{}) {
	podDeleteQueue = workqueue.NewNamedRateLimitingQueue(
		workqueue.DefaultControllerRateLimiter(),
		"podDeletionQueue",
	)
	go func() {
		for {
			// Wait until an item from the work queue is ready
			podToDeleteObj, shutdown := podDeleteQueue.Get()
			if shutdown {
				return
			}
			podToDelete := podToDeleteObj.(PodToDelete)
			podName := strings.Split(podToDelete.Name, ".")[0]
			podNamespace := podToDelete.Namespace
			if !dryRunMode {
				// Delete the Pod from the Kubernetes API
				err := clientSet.CoreV1().Pods(podNamespace).Delete(ctx, podName, metav1.DeleteOptions{})
				if err != nil {
					// If the Pod has already been deleted, do not retry
					if !strings.Contains(err.Error(), "not found") {
						log.Printf("Failed to delete Pod %s/%s: %v\n", podNamespace, podToDelete.Name, err)
						podDeleteQueue.AddRateLimited(podToDelete)
					}
				} else {
					log.Printf("Deleted Pod %s/%s\n", podNamespace, podToDelete.Name)
					podDeleteQueue.Forget(podToDelete)
				}
			} else {
				log.Printf("[DRY-RUN] Would have deleted Pod %s/%s\n", podNamespace, podToDelete.Name)
				podDeleteQueue.Forget(podToDelete)
			}
			podDeleteQueue.Done(podToDelete)
		}
	}()

	// Handle shutdown gracefully
	go func() {
		<-stopCh
		podDeleteQueue.ShutDown()
	}()
}
