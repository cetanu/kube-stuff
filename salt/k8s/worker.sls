include:
  - k8s.common

join_cluster:
  cmd.run:
    - name: |
        until JOIN_CMD=$(aws ssm get-parameter --name "/kubeadm/join-command" --query "Parameter.Value" --output text --region {{ salt['pillar.get']('aws_region') }} 2>/dev/null); do
          sleep 10
        done
        eval "$JOIN_CMD"
    - unless: test -f /etc/kubernetes/kubelet.conf
    - require:
      - pkg: k8s_packages
