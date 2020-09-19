Some services, such as Istio and Envoy's Secret Discovery Service (SDS), take advantage of a new feature in Kubernetes 1.12+, [Service Account Token Volume Projection](https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#service-account-token-volume-projection).


1. In order to enable this feature for Kubernetes 1.12+, add the following config to your cluster spec:

```yaml
    kubeAPIServer:
        apiAudiences:
        - api
        - istio-ca
        serviceAccountIssuer: kubernetes.default.svc
        serviceAccountKeyFile:
        - /srv/kubernetes/server.key
        serviceAccountSigningKeyFile: /srv/kubernetes/server.key
```
