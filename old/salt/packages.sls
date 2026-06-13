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
