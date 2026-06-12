include:
  - .kubeadm
  - .helm

flannel_apply:
  cmd.run:
    - name: |
        curl -sL https://github.com/flannel-io/flannel/releases/latest/download/kube-flannel.yml -o /tmp/kube-flannel.yml
        sed -i "s|10.244.0.0/16|{{ salt['pillar.get']('pod_cidr', '10.244.0.0/16') }}|g" /tmp/kube-flannel.yml
        kubectl --kubeconfig=/etc/kubernetes/admin.conf apply -f /tmp/kube-flannel.yml
    - require:
      - cmd: kubeadm_init

aws-cloud-controller-manager-repo:
  helm.repo_managed:
    - present:
      - name: aws-cloud-controller-manager
        url: https://kubernetes.github.io/cloud-provider-aws

aws-ccm:
  helm.release_present:
    - name: aws-cloud-controller-manager
    - chart: aws-cloud-controller-manager/aws-cloud-controller-manager
    - namespace: kube-system
    - set:
      - hostNetwork=true
      - hostNetworking=true
      - "args[0]=--v=2"
      - "args[1]=--cloud-provider=aws"
      - "args[2]=--cluster-name=kubernetes"
      - "args[3]=--allocate-node-cidrs=true"
      - "args[4]=--cluster-cidr={{ salt['pillar.get']('pod_cidr', '10.244.0.0/16') }}"
      - "args[5]=--configure-cloud-routes=false"
    - require:
      - cmd: helm_install
      - cmd: flannel_apply
      - helm: aws-cloud-controller-manager-repo
      - file: root_kubeconfig

aws-ccm-patch:
  cmd.run:
    - name: kubectl --kubeconfig=/etc/kubernetes/admin.conf patch ds aws-cloud-controller-manager -n kube-system -p '{"spec":{"template":{"spec":{"hostNetwork":true}}}}' || true
    - require:
      - helm: aws-ccm

aws-ebs-csi-driver-repo:
  helm.repo_managed:
    - present:
      - name: aws-ebs-csi-driver
        url: https://kubernetes-sigs.github.io/aws-ebs-csi-driver

ebs_csi_driver:
  helm.release_present:
    - name: aws-ebs-csi-driver
    - chart: aws-ebs-csi-driver/aws-ebs-csi-driver
    - namespace: kube-system
    - require:
      - cmd: helm_install
      - cmd: flannel_apply
      - helm: aws-ebs-csi-driver-repo
      - file: root_kubeconfig

gateway_api_crds_repo:
  helm.repo_managed:
    - present:
      - name: wiremind
        url: https://wiremind.github.io/wiremind-helm-charts

gateway_api_crds:
  helm.release_present:
    - name: gateway-api-crds
    - chart: wiremind/gateway-api-crds
    - version: 1.5.1
    - require:
      - helm: gateway_api_crds_repo
      - cmd: helm_install
      - file: root_kubeconfig

envoy_gateway:
  helm.release_present:
    - name: eg
    - chart: oci://docker.io/envoyproxy/gateway-helm
    - version: v1.8.1
    - namespace: envoy-gateway-system
    - flags:
      - "--create-namespace"
    - require:
      - cmd: helm_install
      - helm: gateway_api_crds
      - file: root_kubeconfig
