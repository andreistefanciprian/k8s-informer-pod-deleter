package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	eventMessage string
	eventReason  string
	dryRunMode   bool
	ctx          = context.TODO()
)

type PodToDelete struct {
	Namespace string
	Name      string
}

var podDeleteQueue = make(chan PodToDelete)

func main() {
	// define and parse cli params
	flag.BoolVar(&dryRunMode, "dry-run", false, "enable dry run mode (no changes are made, only logged)")
	flag.StringVar(&eventReason, "event-reason", "FailedCreatePodSandBox", "restart Pods that match Event Reason")
	flag.StringVar(
		&eventMessage,
		"event-message",
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
	informerFactory := informers.NewSharedInformerFactory(clientSet, time.Minute*1)

	// using this factory create an informer for k8s resources
	k8sObjectInformer := informerFactory.Core().V1().Events()

	// adds an event handler to the shared informer
	k8sObjectInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    onAdd,
		UpdateFunc: onUpdate,
		DeleteFunc: nil,
	})

	go func() {
		for {
			select {
			case pd := <-podDeleteQueue:
				if !dryRunMode {
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

	// starts the shared informers that have been created by the factory
	informerFactory.Start(stopCh)

	// wait for the initial synchronization of the local cache
	if !cache.WaitForCacheSync(stopCh, k8sObjectInformer.Informer().HasSynced) {
		panic("failed to sync")
	}

	// causes the goroutine to block (hit CTRL+C to exit)
	select {}

}

func onAdd(newObj interface{}) {
	item := newObj.(*corev1.Event)
	if item.Reason == eventReason && strings.Contains(item.Message, eventMessage) {
		log.Printf(
			"ADD ResVer(%s): Sending Pod %s/%s to the podDeleteQueue.\n",
			item.GetResourceVersion(),
			item.InvolvedObject.Namespace,
			item.InvolvedObject.Name,
		)
		// put Pod in deletion queue
		podDeleteQueue <- PodToDelete{
			Namespace: item.InvolvedObject.Namespace,
			Name:      item.InvolvedObject.Name,
		}
	}
}

func onUpdate(old, new interface{}) {
	// oldEvent := old.(*corev1.Event)
	item := new.(*corev1.Event)

	if item.Reason == eventReason && strings.Contains(item.Message, eventMessage) {
		log.Printf(
			"UPDATED ResVer(%s): Sending Pod %s/%s to the podDeleteQueue.\n",
			item.GetResourceVersion(),
			item.InvolvedObject.Namespace, item.InvolvedObject.Name,
		)
		// put Pod in deletion queue
		podDeleteQueue <- PodToDelete{
			Namespace: item.InvolvedObject.Namespace,
			Name:      item.InvolvedObject.Name,
		}
	}
}
