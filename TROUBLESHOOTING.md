# Troubleshooting

## Getting Docker working with `firewalld` (Fedora/CentOS/RHEL)
```
firewall-cmd --zone=public --add-masquerade --permanent
firewall-cmd --zone=trusted --change-interface=docker 0 --permanent
firewall-cmd --reload
```

## Receiving Docker network errors

**Symptom:**  Starting of challenge instances fails with the message
"ERROR:  could not create challenge network (cmgr-...): Error response from
daemon:  could not find an available, non-overlapping IPv4 address pool
among the defaults to assign to the network.

**Cause:** Docker has exhausted all available subnets that it has been
assigned and cannot create anymore.  By default, Docker only reserves 31
distinct subnets which constrains `cmgr` to no more than that number of running
challenge instances (each instance gets a network).

**Solution:** Choose a sufficiently large region of RFC 1918 address space
and update the Docker daemon's configuration (`/etc/docker/daemon.json`) to
allot more default networks.  It is important to ensure that these addresses
are not in use by another network segment and that the individual subnets are
large enough to handle any multi-host challenges (to include a solver host and
the default gateway).

An example configuration which carves the address space into \~2 million subnets
is:
```json
{
  "default-address-pools":
    [
      {"base":"10.0.0.0/8", "size":29}
    ]
}
```

**Note:** You will need to restart the daemon after changing its configuration.

## A container fails to create threads or processes

**Symptom:** A challenge using a recent Linux distribution fails while creating
threads or processes, often with an `Operation not permitted` error.

**Cause:** The challenge may have explicitly selected cmgr's historical seccomp
profile with `seccomp.legacy: true`. That profile predates the `clone3` fallback
behavior required by newer versions of glibc.

**Solution:** Remove the legacy setting so Docker applies its current default
profile. If the challenge only needs to disable ASLR, replace it with:

```yaml
seccomp:
    tweaks:
        - allow-disable-aslr
```

The tweak requires the
[`cmgr-oci-interceptor`](README.md#seccomp-oci-interceptor) runtime on the
Docker host.

## A seccomp tweak reports that the OCI runtime is missing

**Symptom:** Starting a challenge configured with `seccomp.tweaks` fails with
an error stating that the `cmgr-oci-interceptor` Docker runtime is required.

**Cause:** The interceptor binary is not installed on the Docker host, or the
named runtime has not been added to that daemon's configuration. Installing
the binary only on a machine acting as a remote Docker client is insufficient.

**Solution:** Install `cmgr-oci-interceptor` on the Docker host, then run:

```sh
sudo cmgr-oci-interceptor register
```

The command updates Docker's daemon configuration and reloads Docker. Confirm
that `cmgr-oci-interceptor` then appears in the daemon's reported runtime list.
