package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	// Kubernetes API types
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	// Kubernetes client libraries
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/client-go/util/workqueue"
)

// Struct to represent a Pod to be deleted
type PodToDelete struct {
	Namespace string // namespace of the Pod
	Name      string // name of the Pod
}

type PodDeleter struct {
	EventMessage   string                          // message used to identify events to be processed
	EventReason    string                          // reason used to identify events to be processed
	DryRun         bool                            // if true, do not actually delete the Pods
	PodDeleteQueue workqueue.RateLimitingInterface // workqueue to hold Pods to be deleted
	EventInformer  coreinformers.EventInformer     // informer to watch for Events
}

var (
	namespace    string           // namespace used to identify events to be processed
	kubeconfig   string           // path to kubeconfig file
	eventMessage string           // message used to identify events to be processed
	eventReason  string           // reason used to identify events to be processed
	dryRunMode   bool             // if true, do not actually delete the Pods
	ctx          = context.TODO() // context used to make Kubernetes API calls
)

func initFlags() {
	// Define and parse command-line flags
	flag.StringVar(&namespace, "namespace", "", "specify Event Namespace to match")
	flag.BoolVar(&dryRunMode, "dry-run", false, "enable dry run mode (no changes are made, only logged)")
	flag.StringVar(&eventReason, "event-reason", "FailedCreatePodSandBox", "specify Event Reason to match")
	flag.StringVar(
		&eventMessage,
		"event-message",
		"container veth name provided (eth0) already exists",
		"specify Event Message to match",
	)

	if home := homedir.HomeDir(); home != "" {
		flag.StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	}

	flag.Parse()
}

func main() {
	// Parse command-line flags
	initFlags()

	// Create k8s API client
	clientSet, err := NewClient(kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	// Create a new shared informer factory
	informerFactory := informers.NewFilteredSharedInformerFactory(clientSet, time.Minute*10, namespace, nil)

	pd := &PodDeleter{
		EventMessage: eventMessage,
		EventReason:  eventReason,
		DryRun:       dryRunMode,
	}

	// Configure Event informer
	err = pd.NewInformer(informerFactory)
	if err != nil {
		panic(err.Error())
	}

	// Start the worker to handle the deletion of Pods from the work queue
	stopCh := make(chan struct{})
	defer close(stopCh)
	pd.startPodDeletionWorker(clientSet, stopCh)

	// Start Event Informer
	err = pd.StartInformer(informerFactory, stopCh)
	if err != nil {
		panic(err.Error())
	}

	// Causes the goroutine to block (hit CTRL+C to exit)
	select {}
}

// NewClient discovers if kubeconfig creds are inside a Pod or outside the cluster and returns a clientSet
func NewClient(kubeconfig string) (kubernetes.Interface, error) {
	// read and parse kubeconfig
	config, err := rest.InClusterConfig() // creates the in-cluster config
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig) // creates the out-cluster config
		if err != nil {
			msg := fmt.Sprintf("The kubeconfig cannot be loaded: %v\n", err)
			return nil, errors.New(msg)
		}
		log.Println("Running from OUTSIDE the cluster")
	} else {
		log.Println("Running from INSIDE the cluster")
	}

	// create the clientset for in-cluster/out-cluster config
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		msg := fmt.Sprintf("The clientset cannot be created: %v\n", err)
		return nil, errors.New(msg)
	}

	return clientset, nil
}

// NewInformer creates a new informer for Kubernetes events
func (c *PodDeleter) NewInformer(informer informers.SharedInformerFactory) error {

	// Use the factory to create an informer for Kubernetes events
	c.EventInformer = informer.Core().V1().Events()

	// Add an event handler to the shared informer
	c.EventInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.onAdd,
		UpdateFunc: c.onUpdate,
		DeleteFunc: nil,
	})

	return nil
}

// StartInformer starts the informer
func (c *PodDeleter) StartInformer(informer informers.SharedInformerFactory, stopCh chan struct{}) error {

	// Start the shared informers that have been created by the factory
	informer.Start(stopCh)

	// Wait for the initial synchronization of the local cache
	if !cache.WaitForCacheSync(stopCh, c.EventInformer.Informer().HasSynced) {
		return errors.New("failed to sync")
	}

	return nil
}

// Handler function to process new events
func (c *PodDeleter) onAdd(new interface{}) {
	event := new.(*corev1.Event)
	c.processEvent(event)
}

// Handler function to process updated events
func (c *PodDeleter) onUpdate(_, new interface{}) {
	event := new.(*corev1.Event)
	c.processEvent(event)
}

func (c *PodDeleter) processEvent(event *corev1.Event) {
	// Check if the event reason and message match the configured values
	if event.Reason == c.EventReason && strings.Contains(event.Message, c.EventMessage) {

		// Put the Pod in the deletion queue
		c.PodDeleteQueue.Add(PodToDelete{
			Namespace: event.GetNamespace(),
			Name:      event.GetName(),
		})
	}
}

func (c *PodDeleter) startPodDeletionWorker(clientSet kubernetes.Interface, stopCh <-chan struct{}) {
	c.PodDeleteQueue = workqueue.NewNamedRateLimitingQueue(
		workqueue.DefaultControllerRateLimiter(),
		"podDeletionQueue",
	)
	go func() {
		for {
			// Wait until an item from the work queue is ready
			podToDeleteObj, shutdown := c.PodDeleteQueue.Get()
			if shutdown {
				return
			}
			podToDelete := podToDeleteObj.(PodToDelete)
			podName := strings.Split(podToDelete.Name, ".")[0]
			podNamespace := podToDelete.Namespace
			if !c.DryRun {
				// Delete the Pod from the Kubernetes API
				err := clientSet.CoreV1().Pods(podNamespace).Delete(ctx, podName, metav1.DeleteOptions{})
				if err != nil {
					// If the Pod has already been deleted, do not retry
					if !strings.Contains(err.Error(), "not found") {
						log.Printf("Failed to delete Pod %s/%s: %v\n", podNamespace, podToDelete.Name, err)
						c.PodDeleteQueue.AddRateLimited(podToDelete)
					}
				} else {
					log.Printf("Deleted Pod %s/%s\n", podNamespace, podToDelete.Name)
					c.PodDeleteQueue.Forget(podToDelete)
				}
			} else {
				log.Printf("[DRY-RUN] Would have deleted Pod %s/%s\n", podNamespace, podToDelete.Name)
				c.PodDeleteQueue.Forget(podToDelete)
			}
			c.PodDeleteQueue.Done(podToDelete)
		}
	}()

	// Handle shutdown gracefully
	go func() {
		<-stopCh
		c.PodDeleteQueue.ShutDown()
	}()
}
