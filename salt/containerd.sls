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
