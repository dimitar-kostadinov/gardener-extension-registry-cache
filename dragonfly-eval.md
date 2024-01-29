# Evaluate Dragonfly

Ref: 
 - https://d7y.io/docs/next/getting-started/quick-start/kubernetes/
 - https://github.com/dragonflyoss/Dragonfly2

Dragonfly is P2P-based file distribution system and consists of several components:
- Manager - Maintain the relationship between each P2P cluster
- Scheduler - Select the optimal download parent peer for the download peer
- Peer - Deploy with dfdaemon provides the dfget command download tool
- Seed Peer - optional peer that can be used as a back-to-source download peer in a P2P cluster

Gardener local setup is used for evaluation
1. Update the /etc/hosts with following where `10.65.64.227` is the local IP address:
   ```
   10.65.64.227 dragonfly-scheduler.external.local.gardener.cloud
   10.65.64.227 dragonfly-manager.external.local.gardener.cloud
   ```
   and update kind extraPortMappings:
   ```
   % git diff example/gardener-local/kind/cluster/templates/_extra_port_mappings.tpl
   diff --git a/example/gardener-local/kind/cluster/templates/_extra_port_mappings.tpl b/example/gardener-local/kind/cluster/templates/_extra_port_mappings.tpl
   index 77200d907..da354208a 100644
   --- a/example/gardener-local/kind/cluster/templates/_extra_port_mappings.tpl
   +++ b/example/gardener-local/kind/cluster/templates/_extra_port_mappings.tpl
   @@ -52,3 +52,10 @@
      protocol: TCP
    {{- end -}}
    {{- end -}}
   +
   +{{- define "extraPortMappings.dragonfly" -}}
   +- containerPort: 30802
   +  hostPort: 8002
   +- containerPort: 30653
   +  hostPort: 65003
   +{{- end -}}
   \ No newline at end of file
   % git diff example/gardener-local/kind/cluster/templates/cluster.yaml
   diff --git a/example/gardener-local/kind/cluster/templates/cluster.yaml b/example/gardener-local/kind/cluster/templates/cluster.yaml
   index 49b9231fe..fd3e1c4cd 100644
   --- a/example/gardener-local/kind/cluster/templates/cluster.yaml
   +++ b/example/gardener-local/kind/cluster/templates/cluster.yaml
   @@ -12,6 +12,7 @@ nodes:
    {{ include "extraPortMappings.gardener.seed.istio" . | indent 2 }}
    {{ include "extraPortMappings.registry" . | indent 2 }}
    {{ include "extraPortMappings.gardener.seed.dns" . | indent 2 }}
   +{{ include "extraPortMappings.dragonfly" . | indent 2 }}
      extraMounts:
    {{ include "extraMounts.gardener.controlPlane" . | indent 2 }}
    {{ include "extraMounts.backupBucket" . | indent 2 }}
   ```
2. From https://github.com/gardener/gardener run make kind-up & make gardener-up
3. From https://github.com/dimitar-kostadinov/gardener-extension-registry-cache `dragonfly-eval` run make extension-up
   In `dragonfly-eval` branch the containerd configuration is added in the `OSC` and a dummy `test` namespace is created 
4. From https://github.com/dragonflyoss/helm-charts add nodePorts
   ```
   (⎈|kind-gardener-local:N/A)I024114@L7KM14M0JY helm-charts % git diff
   diff --git a/charts/dragonfly/templates/manager/manager-svc.yaml b/charts/dragonfly/templates/manager/manager-svc.yaml
   index 2499e75..cc482b8 100644
   --- a/charts/dragonfly/templates/manager/manager-svc.yaml
   +++ b/charts/dragonfly/templates/manager/manager-svc.yaml
   @@ -27,6 +27,7 @@ spec:
          name: http-grpc
          protocol: TCP
          targetPort: {{ .Values.manager.grpcPort }}
   +      nodePort: 30653
      selector:
        app: {{ template "dragonfly.fullname" . }}
        release: {{ .Release.Name }}
   diff --git a/charts/dragonfly/templates/scheduler/scheduler-svc.yaml b/charts/dragonfly/templates/scheduler/scheduler-svc.yaml
   index 6584f83..c59f2fe 100644
   --- a/charts/dragonfly/templates/scheduler/scheduler-svc.yaml
   +++ b/charts/dragonfly/templates/scheduler/scheduler-svc.yaml
   @@ -23,6 +23,7 @@ spec:
          name: http-grpc
          protocol: TCP
          targetPort: {{ .Values.scheduler.config.server.port }}
   +      nodePort: 30802
      selector:
        app: {{ template "dragonfly.fullname" . }}
        release: {{ .Release.Name }}   
   ```
   and deploy Manager and Scheduler in Control plane using the following charts-config-seed.yaml:
   `helm install --wait --create-namespace --namespace dragonfly-system dragonfly charts/dragonfly --values ../charts-config-seed.yaml` 
   ```yaml
   scheduler:
     enable: true
     image: dragonflyoss/scheduler
     tag: latest
     replicas: 1
     metrics:
       enable: true
     config:
       console: true
       verbose: true
       pprofPort: 18066
       seedPeer:
         enable: false
     service:
       type: NodePort
   
   seedPeer:
     enable: false
   
   dfdaemon:
     enable: false
   
   manager:
     enable: true
     image: dragonflyoss/manager
     tag: latest
     replicas: 1
     metrics:
       enable: true
     config:
       console: true
       verbose: true
       pprofPort: 18066
     service:
       type: NodePort
   
   jaeger:
     enable: false
   
   redis:
     enable: true
   
   mysql:
     enable: true
   ```
5. From https://github.com/dimitar-kostadinov/gardener-extension-registry-cache create a shoot `% k create -f example/shoot.yaml`
6. Create shoot kube config `k create -f ~/go/src/github.com/gardener/kubeconfig-request.json --raw /apis/core.gardener.cloud/v1beta1/namespaces/garden-local/shoots/local/adminkubeconfig | jq -r ".status.kubeconfig" |  base64 -d > localadm.kubeconfig` and target the shoot.
7. From https://github.com/dragonflyoss/helm-charts deploy Dfdaemon `% helm install --wait --create-namespace --namespace dragonfly-system dragonfly helm-charts/charts/dragonfly --values ../charts-config-shoot.yaml` with following charts-config-shoot.yaml:
   ```yaml
   scheduler:
     enable: false
   
   seedPeer:
     enable: false
   
   dfdaemon:
     priorityClassName: system-node-critical
     image: dragonflyoss/dfdaemon
     tag: latest
     hostNetwork: true
     metrics:
       enable: true
     config:
       console: true
       verbose: true
       pprofPort: 18066
       keepStorage: true
       scheduler:
         manager:
           enable: false
         netAddrs:
           - type: tcp
             addr: dragonfly-scheduler.external.local.gardener.cloud:8002
     tolerations:
     - effect: NoSchedule
       operator: Exists
     - effect: NoExecute
       operator: Exists
     - effect: NoExecute
       key: node.kubernetes.io/not-ready
       operator: Exists
     - effect: NoExecute
       key: node.kubernetes.io/unreachable
       operator: Exists
     - effect: NoSchedule
       key: node.kubernetes.io/disk-pressure
       operator: Exists
     - effect: NoSchedule
       key: node.kubernetes.io/memory-pressure
       operator: Exists
     - effect: NoSchedule
       key: node.kubernetes.io/pid-pressure
       operator: Exists
     - effect: NoSchedule
       key: node.kubernetes.io/unschedulable
       operator: Exists
     - effect: NoSchedule
       key: node.kubernetes.io/network-unavailable
       operator: Exists
   
   
   externalManager:
     enable: true
     host: dragonfly-manager.external.local.gardener.cloud
     restPort: 8080
     grpcPort: 65003
   
   manager:
     enable: false
   
   jaeger:
     enable: true
   
   redis:
     enable: false
   
   mysql:
     enable: false
   ```
8. Test `% k run test --image nginx` & check logs
   ```
   % k -n dragonfly-system logs dragonfly-dfdaemon-k86qd | grep nginx
   2024-01-29T14:09:05.891Z	DEBUG	transport/transport.go:201	round trip directly, method: HEAD, url: https://registry-1.docker.io/v2/library/nginx/manifests/latest?ns=docker.io
   2024-01-29T14:09:06.870Z	DEBUG	transport/transport.go:201	round trip directly, method: HEAD, url: https://registry-1.docker.io/v2/library/nginx/manifests/latest?ns=docker.io
   2024-01-29T14:09:07.319Z	DEBUG	transport/transport.go:201	round trip directly, method: GET, url: https://registry-1.docker.io/v2/library/nginx/manifests/sha256:4c0fdaa8b6341bfdeca5f18f7837462c80cff90527ee35ef185571e1c327beac?ns=docker.io
   2024-01-29T14:09:08.088Z	DEBUG	transport/transport.go:201	round trip directly, method: GET, url: https://registry-1.docker.io/v2/library/nginx/manifests/sha256:4c0fdaa8b6341bfdeca5f18f7837462c80cff90527ee35ef185571e1c327beac?ns=docker.io
   2024-01-29T14:09:08.522Z	DEBUG	transport/transport.go:201	round trip directly, method: GET, url: https://registry-1.docker.io/v2/library/nginx/manifests/sha256:523c417937604bc107d799e5cad1ae2ca8a9fd46306634fa2c547dc6220ec17c?ns=docker.io
   2024-01-29T14:09:08.943Z	DEBUG	transport/transport.go:197	round trip with dragonfly: https://registry-1.docker.io/v2/library/nginx/blobs/sha256:6c7be49d2a11cfab9a87362ad27d447b45931e43dfa6919a8e1398ec09c1e353?ns=docker.io
   2024-01-29T14:09:08.943Z	INFO	transport/transport.go:243	start download with url: https://registry-1.docker.io/v2/library/nginx/blobs/sha256:6c7be49d2a11cfab9a87362ad27d447b45931e43dfa6919a8e1398ec09c1e353?ns=docker.io	{"peer": "10.1.131.13-1-c99a4ac4-81d5-47cb-9847-ec539c4945e7", "component": "transport", "trace": "33e10a247011c72e715958b43932d3ae"}
   2024-01-29T14:09:08.943Z	DEBUG	transport/transport.go:244	request url: https://registry-1.docker.io/v2/library/nginx/blobs/sha256:6c7be49d2a11cfab9a87362ad27d447b45931e43dfa6919a8e1398ec09c1e353?ns=docker.io, with header: http.Header{"Accept":[]string{"application/vnd.oci.image.config.v1+json, */*"}, "Authorization":[]string{"Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCIsIng1YyI6WyJNSUlFRmpDQ0F2NmdBd0lCQWdJVVZOajJRbU1JWnUzeGl0NUJ1RTlvRWdoVU5KUXdEUVlKS29aSWh2Y05BUUVMQlFBd2dZWXhDekFKQmdOVkJBWVRBbFZUTVJNd0VRWURWUVFJRXdwRFlXeHBabTl5Ym1saE1SSXdFQVlEVlFRSEV3bFFZV3h2SUVGc2RHOHhGVEFUQmdOVkJBb1RERVJ2WTJ0bGNpd2dTVzVqTGpFVU1CSUdBMVVFQ3hNTFJXNW5hVzVsWlhKcGJtY3hJVEFmQmdOVkJBTVRHRVJ2WTJ0bGNpd2dTVzVqTGlCRmJtY2dVbTl2ZENCRFFUQWVGdzB5TkRBeE1UWXdOak0yTURCYUZ3MHlOVEF4TVRVd05qTTJNREJhTUlHRk1Rc3dDUVlEVlFRR0V3SlZVekVUTUJFR0ExVUVDQk1LUTJGc2FXWnZjbTVwWVRFU01CQUdBMVVFQnhNSlVHRnNieUJCYkhSdk1SVXdFd1lEVlFRS0V3eEViMk5yWlhJc0lFbHVZeTR4RkRBU0JnTlZCQXNUQzBWdVoybHVaV1Z5YVc1bk1TQXdIZ1lEVlFRREV4ZEViMk5yWlhJc0lFbHVZeTRnUlc1bklFcFhWQ0JEUVRDQ0FTSXdEUVlKS29aSWh2Y05BUUVCQlFBRGdnRVBBRENDQVFvQ2dnRUJBTWI4eHR6ZDQ1UWdYekV0bWMxUEJsdWNGUnlzSUF4UUJCN3lSNjdJemdMd05IS24rbUdKTzV5alh6amtLZm5zWm1JRURnZFlraEpBbGNYYTdQa1BFaCtqcTRGNWNaaWtkTmFUQmM3alNkTFJzTVlVa3dwWTl4WUVqYitCYnVGUWVxa0R2RXNqbFJJTzRQK0FsRlhNMDhMYlpIZ3hFWUdkbFk3WFlhT1BLMmE1aUd2eVFRb09GVmZjZDd2ekhaREVBMHZqVmU1M0xLdjVMYmh6TzcxZHRxS0RwNEhnVWR5N1pENDFNN3I1bTd5eE1LeFNpQmJHZTFvem5Wamh1ck5GNHdGSml5bVU4YkhTV2tVTXVLQ3JTbEd4d1NCZFVZNDRyaEh2UW5zYmgzUFF2TUZTWTQ4REdoNFhUUldjSzFWUVlSTnA2ZWFXUVg1RUpJSXVJbjJQOVBzQ0F3RUFBYU43TUhrd0RnWURWUjBQQVFIL0JBUURBZ0dtTUJNR0ExVWRKUVFNTUFvR0NDc0dBUVVGQndNQk1CSUdBMVVkRXdFQi93UUlNQVlCQWY4Q0FRQXdIUVlEVlIwT0JCWUVGSnVRYXZTZHVScm5kRXhLTTAwV2Z2czh5T0RaTUI4R0ExVWRJd1FZTUJhQUZGSGVwRE9ZQ0Y5Qnc5dXNsY0RVUW5CalU3MS9NQTBHQ1NxR1NJYjNEUUVCQ3dVQUE0SUJBUUNDWW0xUVorUUZ1RVhkSWpiNkg4bXNyVFBRSlNnR0JpWDFXSC9QRnpqZlJGeHc3dTdDazBRb0FXZVNqV3JWQWtlVlZQN3J2REpwZ0ZoeUljdzNzMXRPVjN0OGp3cXJTUmc2R285dUd2OG9IWUlLTm9YaDErUFVDTG44b0JwYUJsOUxzSWtsL2FHMG9lY0hrcDVLYmtBNjN6eTFxSUVXNFAzWVJLSk9hSGoxYWFiOXJLc3lRSHF6SUl4TnlDRVVINTMwU1B4RUNMbE53YWVKTDVmNXIxUW5wSi9GM3Q5Vk8xZ0Y2RFpiNitPczdTV29ocGhWZlRCOERkL1VjSk1VOGp2YlF3MWRVREkwelNEdXo2aHNJbGdITk0yak04M0lOS1VqNjNaRDMwRG15ejQvczFFdGgyQmlKK2RHdnFpQkRzaWhaR0tyQnJzUzhWVkRBd3hDeDVRMyJdfQ.eyJhY2Nlc3MiOlt7ImFjdGlvbnMiOlsicHVsbCJdLCJuYW1lIjoibGlicmFyeS9uZ2lueCIsInBhcmFtZXRlcnMiOnsicHVsbF9saW1pdCI6IjEwMCIsInB1bGxfbGltaXRfaW50ZXJ2YWwiOiIyMTYwMCJ9LCJ0eXBlIjoicmVwb3NpdG9yeSJ9XSwiYXVkIjoicmVnaXN0cnkuZG9ja2VyLmlvIiwiZXhwIjoxNzA2NTM3NjQ4LCJpYXQiOjE3MDY1MzczNDgsImlzcyI6ImF1dGguZG9ja2VyLmlvIiwianRpIjoiZGNrcl9qdGlfQ3c0ZVoxSWZGbFVNcGEtdi1aU1dubnRxSEJ3PSIsIm5iZiI6MTcwNjUzNzA0OCwic3ViIjoiIn0.teIyNsti-36ES9TcAKE4LpIQ_g7cXHq5nMRPYuEF4BtLu8RKXV947snljikq6eInud8eyEqfgGs7KW6odLMglvM5oS3NRyot8ooGEtnNies-L2F6iKAhDcqtJ7NWnDOvza_QPlAyODZhMFzzvE2Vdrp2juR2YpeNHrRHyHJ_2D0NHGM_CdUn_H6roA8so6rr9WMBx5szec5gyjdrLrpxpZmTfKURlyw7SdxWh0VJ2cPtxAzOl8Lg3B5cz6a0p4leP2vz6BEBOCsxgpjNqPmfSt-rMlgyo6NnhkpxPXaPlyypbc8weK-8wh6uJ34WcSLR7QRfXflnchw4N46waBLQaw"}, "User-Agent":[]string{"containerd/v1.7.1"}, "X-Dragonfly-Registry":[]string{"https://registry-1.docker.io"}, "X-Forwarded-For":[]string{"127.0.0.1"}}	{"peer": "10.1.131.13-1-c99a4ac4-81d5-47cb-9847-ec539c4945e7", "component": "transport", "trace": "33e10a247011c72e715958b43932d3ae"}
   ```
   
If the `Dfget` is run as systemd unit service it can potentially cache all the images. 