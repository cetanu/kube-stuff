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

run_salt_script:
  file.managed:
    - name: /usr/local/bin/run-salt.sh
    - mode: "0755"
    - template: jinja
    - contents: |
        #!/bin/bash
        pushd /srv/salt-repo
            git fetch origin
            LOCAL=$(git rev-parse HEAD)
            REMOTE=$(git rev-parse origin/$(git rev-parse --abbrev-ref HEAD))
            if [ "$LOCAL" == "$REMOTE" ] && [ -f /var/run/salt-bootstrapped ]; then
              exit 0
            fi
            git reset --hard $REMOTE
        popd
        salt-call --local --file-root=/srv/salt-repo/salt state.apply pillar="{aws_region: '{{ salt['pillar.get']('aws_region') }}', eip: '{{ salt['pillar.get']('eip', '') }}'}"
        touch /var/run/salt-bootstrapped
        
        ROLE=$(cat /etc/salt/grains | grep role | awk '{print $2}')
        if [ "$ROLE" == "controlplane" ]; then
          aws ssm put-parameter --name "/kubeadm/salt-status" --value "$REMOTE" --type "String" --overwrite --region {{ salt['pillar.get']('aws_region') }}
        fi

run_salt_cron:
  cron.present:
    - name: /usr/local/bin/run-salt.sh >> /var/log/salt-cron.log 2>&1
    - user: root
    - minute: '*'
