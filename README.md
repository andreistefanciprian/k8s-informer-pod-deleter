# k8s_informer

Build a k8s informer that monitors k8s EVents and deletes Pods that have matching Event Reason and Event Message.

```
# run script
go run main.go --event-reason BackOff --event-message="Back-off pulling image"

# create failing Pods
for i in {1..5};do kubectl run foo-$i --image nginx:bad; done

# delete pods
for pod in `kubectl get pods --no-headers | awk '{print $1}'`; do kubectl delete pod $pod --now; done
```
