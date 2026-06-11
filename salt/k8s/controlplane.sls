include:
  - k8s.common

helm_install:
  cmd.run:
    - name: curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
    - creates: /usr/local/bin/helm

kubeadm_config:
  file.managed:
    - name: /etc/kubernetes/kubeadm-config.yaml
    - template: jinja
    - contents: |
        apiVersion: kubeadm.k8s.io/v1beta3
        kind: InitConfiguration
        localAPIEndpoint:
          advertiseAddress: 10.240.0.11
        nodeRegistration:
          kubeletExtraArgs:
            cloud-provider: external
        ---
        apiVersion: kubeadm.k8s.io/v1beta3
        kind: ClusterConfiguration
        kubernetesVersion: "1.30.0"
        clusterName: "kubernetes"
        networking:
          podSubnet: "10.244.0.0/16"
        apiServer:
          certSANs:
            - "10.240.0.11"
            - "{{ salt['pillar.get']('eip') }}"
          extraArgs:
            cloud-provider: external
        controllerManager:
          extraArgs:
            cloud-provider: external

kubeadm_init:
  cmd.run:
    - name: kubeadm init --config /etc/kubernetes/kubeadm-config.yaml
    - creates: /etc/kubernetes/admin.conf
    - require:
      - file: kubeadm_config
      - pkg: k8s_packages

kubeconfig_setup:
  cmd.run:
    - name: |
        mkdir -p /home/ubuntu/.kube
        cp -i /etc/kubernetes/admin.conf /home/ubuntu/.kube/config
        chown -R ubuntu:ubuntu /home/ubuntu/.kube
    - creates: /home/ubuntu/.kube/config
    - require:
      - cmd: kubeadm_init

flannel_apply:
  cmd.run:
    - name: kubectl --kubeconfig=/etc/kubernetes/admin.conf apply -f https://github.com/flannel-io/flannel/releases/latest/download/kube-flannel.yml
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
    - kubeconfig: /etc/kubernetes/admin.conf
    - set:
      - name: hostNetworking
        value: "true"
      - name: args[0]
        value: "--v=2"
      - name: args[1]
        value: "--cloud-provider=aws"
    - require:
      - cmd: helm_install
      - cmd: flannel_apply
      - helm: aws-cloud-controller-manager-repo

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
    - kubeconfig: /etc/kubernetes/admin.conf
    - require:
      - cmd: helm_install
      - cmd: flannel_apply
      - helm: aws-ebs-csi-driver-repo

gateway_api_crds:
  cmd.run:
    - name: |
        export KUBECONFIG=/etc/kubernetes/admin.conf
        kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.1.0/experimental-install.yaml
    - require:
      - cmd: kubeadm_init

envoy_gateway_ns:
  cmd.run:
    - name: kubectl create ns envoy-gateway-system --kubeconfig=/etc/kubernetes/admin.conf
    - unless: kubectl get ns envoy-gateway-system --kubeconfig=/etc/kubernetes/admin.conf

envoy_gateway:
  helm.release_present:
    - name: eg
    - chart: oci://docker.io/envoyproxy/gateway-helm
    - version: v1.1.1
    - namespace: envoy-gateway-system
    - kubeconfig: /etc/kubernetes/admin.conf
    - require:
      - cmd: helm_install
      - cmd: gateway_api_crds
      - cmd: envoy_gateway_ns

ssm_kubeconfig:
  cmd.run:
    - name: |
        cp /etc/kubernetes/admin.conf /tmp/kubeconfig.yaml
        sed -i "s/10.240.0.11/{{ salt['pillar.get']('eip') }}/g" /tmp/kubeconfig.yaml
        aws ssm put-parameter --name "/kubeadm/kubeconfig" --value "$(cat /tmp/kubeconfig.yaml)" --type "String" --tier "Intelligent-Tiering" --overwrite --region {{ salt['pillar.get']('aws_region') }}
    - require:
      - cmd: kubeadm_init

ssm_join_command:
  cmd.run:
    - name: |
        JOIN_CMD=$(kubeadm token create --print-join-command)
        aws ssm put-parameter --name "/kubeadm/join-command" --value "$JOIN_CMD" --type "String" --overwrite --region {{ salt['pillar.get']('aws_region') }}
    - require:
      - cmd: kubeadm_init

k8s_maintenance_script:
  file.managed:
    - name: /usr/local/bin/k8s-maintenance.sh
    - mode: "0755"
    - contents: |
        #!/bin/bash
        set -e
        echo "Starting Kubernetes maintenance and cert renewal..."
        kubeadm certs renew all
        systemctl restart kubelet
        until kubectl get nodes --raw /healthz >/dev/null 2>&1; do
          sleep 2
        done
        cp -i /etc/kubernetes/admin.conf /home/ubuntu/.kube/config
        chown ubuntu:ubuntu /home/ubuntu/.kube/config
        cp /etc/kubernetes/admin.conf /tmp/kubeconfig.yaml
        sed -i "s/10.240.0.11/{{ salt['pillar.get']('eip') }}/g" /tmp/kubeconfig.yaml
        aws ssm put-parameter --name "/kubeadm/kubeconfig" --value "$(cat /tmp/kubeconfig.yaml)" --type "String" --tier "Intelligent-Tiering" --overwrite --region {{ salt['pillar.get']('aws_region') }}
        echo "Kubernetes maintenance completed successfully."

k8s_maintenance_cron:
  cron.present:
    - name: /usr/local/bin/k8s-maintenance.sh >> /var/log/k8s-maintenance.log 2>&1
    - user: root
    - minute: '0'
    - hour: '0'
    - dayweek: '0'
    - require:
      - file: k8s_maintenance_script
