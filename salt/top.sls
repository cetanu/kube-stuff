base:
  'role:controlplane':
    - match: grain
    - k8s.controlplane

  'role:worker':
    - match: grain
    - k8s.worker
