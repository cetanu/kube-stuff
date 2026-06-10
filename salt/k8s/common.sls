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

containerd_install:
  pkg.installed:
    - name: containerd

containerd_config_dir:
  file.directory:
    - name: /etc/containerd
    - require:
      - pkg: containerd_install

containerd_config:
  cmd.run:
    - name: containerd config default > /etc/containerd/config.toml
    - creates: /etc/containerd/config.toml
    - require:
      - file: containerd_config_dir

containerd_systemd_cgroup:
  file.replace:
    - name: /etc/containerd/config.toml
    - pattern: 'SystemdCgroup = false'
    - repl: 'SystemdCgroup = true'
    - require:
      - cmd: containerd_config

containerd_service:
  service.running:
    - name: containerd
    - enable: True
    - watch:
      - file: containerd_systemd_cgroup

k8s_prereqs:
  pkg.installed:
    - pkgs:
      - apt-transport-https
      - ca-certificates
      - curl
      - gpg

k8s_keyring:
  cmd.run:
    - name: curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.30/deb/Release.key | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
    - creates: /etc/apt/keyrings/kubernetes-apt-keyring.gpg
    - require:
      - pkg: k8s_prereqs

k8s_repo:
  file.managed:
    - name: /etc/apt/sources.list.d/kubernetes.list
    - contents: 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.30/deb/ /'
    - require:
      - cmd: k8s_keyring

k8s_packages:
  pkg.installed:
    - pkgs:
      - kubelet
      - kubeadm
      - kubectl
      - awscli
    - refresh: True
    - require:
      - file: k8s_repo

k8s_hold:
  cmd.run:
    - name: apt-mark hold kubelet kubeadm kubectl
    - onchanges:
      - pkg: k8s_packages
