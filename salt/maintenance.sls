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
