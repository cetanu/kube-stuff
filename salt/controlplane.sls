kubelet_config:
  file.managed:
    - name: /etc/default/kubelet
    - contents: |
        KUBELET_EXTRA_ARGS=--cloud-provider=external
    - require:
      - pkg: k8s_packages

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
            provider-id: "{{ salt['grains.get']('provider_id') }}"
        ---
        apiVersion: kubeadm.k8s.io/v1beta3
        kind: ClusterConfiguration
        kubernetesVersion: "1.30.0"
        clusterName: "kubernetes"
        networking:
          podSubnet: "{{ salt['pillar.get']('pod_cidr', '10.244.0.0/16') }}"
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

root_kubeconfig:
  file.symlink:
    - name: /root/.kube/config
    - target: /etc/kubernetes/admin.conf
    - makedirs: True
    - require:
      - cmd: kubeadm_init

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
