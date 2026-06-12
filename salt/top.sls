base:
  'role:controlplane':
    - match: grain
    - sysctl
    - containerd
    - packages
    - salt-sync
    - helm
    - addons
    - ssm
    - maintenance
    - controlplane

  'role:worker':
    - match: grain
    - sysctl
    - containerd
    - packages
    - salt-sync
    - worker
