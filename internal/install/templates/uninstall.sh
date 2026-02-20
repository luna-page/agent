#!/bin/bash

echo "Stopping and disabling the service..."
systemctl disable --now {{ .ServiceName }}

{{ if .AddFirewallRule }}
echo -e "\nRemoving firewall rule..."
ufw delete allow {{ .Port }}/tcp
{{ end }}

echo -e "\nConfirm one at a time if you want to remove the following 5 files and 1 directory [y/n]:\n"
rm -i \
    "{{ .ConfigPath }}" \
    "{{ .BinaryPath }}" \
    "{{ .ServicePath }}" \
    "{{ .UninstallScriptPath }}" \
    "{{ .UpdateScriptPath }}"

rm -di "{{ .InstallDirectory }}"
