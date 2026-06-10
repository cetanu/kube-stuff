include:
  - k8s.common

helm_install:
  cmd.run:
    - name: curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
    - creates: /usr/local/bin/helm

kubeadm_init:
  cmd.run:
    - name: kubeadm init --apiserver-advertise-address=10.240.0.11 --pod-network-cidr=10.244.0.0/16 --apiserver-cert-extra-sans=10.240.0.11,{{ salt['pillar.get']('eip') }}
    - creates: /etc/kubernetes/admin.conf
    - require:
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

ebs_csi_driver:
  cmd.run:
    - name: |
        export KUBECONFIG=/etc/kubernetes/admin.conf
        helm repo add aws-ebs-csi-driver https://kubernetes-sigs.github.io/aws-ebs-csi-driver
        helm repo update
        helm install aws-ebs-csi-driver aws-ebs-csi-driver/aws-ebs-csi-driver --namespace kube-system
    - require:
      - cmd: helm_install
      - cmd: flannel_apply
    - unless: helm ls -n kube-system | grep aws-ebs-csi-driver

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
