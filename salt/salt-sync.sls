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
