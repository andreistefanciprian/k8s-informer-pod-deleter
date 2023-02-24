# Description

This program written in Go monitors Kubernetes events, identifies Pods that meet specific Event Reason and Event Message and then deletes them. 

It uses the Kubernetes API and client libraries to manage a shared informer, which is used to monitor events. 

The program uses a command-line option to enable dry run mode and log actions without changing the cluster.

## CLI params

#### `--event-reason`
- sets the eventReason variable in the program
- default value: 'FailedCreatePodSandBox'

#### `--event-message`
- sets the eventMessage variable in the program
- default value: 'container veth name provided (eth0) already exists'

#### `--dry-run`
- enable dry run mode and log actions without changing the cluster
- this option is disabled (set to false) by default

## Run and test on local machine

```
# install dependencies
go mod init
go mod tidy

# compile source code into executable binary
go build -o pod-deleter

# run program in dry run mode
./pod-deleter --dry-run

# run program
./pod-deleter --event-reason BackOff --event-message="Back-off pulling image"

# generate failing pods to test if program is deleting the Pods
for i in {1..5};do kubectl run foo-$i --image nginx:bad; done

# delete pods
for pod in `kubectl get pods --no-headers | awk '{print $1}'`; do kubectl delete pod $pod --now; done
```