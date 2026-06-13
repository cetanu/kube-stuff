base:
  'role:controlplane':
    - match: grain
    - sysctl
    - containerd
    - packages
    - salt-sync
    - controlplane

  'role:worker':
    - match: grain
    - sysctl
    - containerd
    - packages
    - salt-sync
    - worker
