include:
  - .kubeadm

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
