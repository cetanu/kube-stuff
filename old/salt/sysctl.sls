k8s_modules:
  file.managed:
    - name: /etc/modules-load.d/k8s.conf
    - contents: |
        overlay
        br_netfilter
  cmd.run:
    - name: modprobe overlay && modprobe br_netfilter
    - onchanges:
      - file: k8s_modules

k8s_sysctl:
  file.managed:
    - name: /etc/sysctl.d/99-kubernetes-cri.conf
    - contents: |
        net.bridge.bridge-nf-call-iptables  = 1
        net.ipv4.ip_forward                 = 1
        net.bridge.bridge-nf-call-ip6tables = 1
  cmd.run:
    - name: sysctl --system
    - onchanges:
      - file: k8s_sysctl
