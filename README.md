# my kubernetes configuration

This is a learning environment for me to become accustomed to k8s, but also
something I may use to host some stuff if it requires the overheads of
kubernetes.

It currently provisions infrastructure in AWS using Pulumi. The kubernetes
cluster is set up on machines which run Talos linux, and those nodes then
reconcile their configs via ArgoCD pointing back at this repo.
