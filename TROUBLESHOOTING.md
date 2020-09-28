# Troubleshooting

## Getting Docker working with `firewalld` (Fedora/CentOS/RHEL)
```
firewall-cmd --zone=public --add-masquerade --permanent
firewall-cmd --zone=trusted --change-interface=docker 0 --permanent
firewall-cmd --reload
```
