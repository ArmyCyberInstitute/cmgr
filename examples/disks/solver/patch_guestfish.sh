#!/bin/sh

# This is a dirty patch to undo a recent patch in libguestfs that prevents
# degraded array from being mounted inside of a guestfish session.

/usr/bin/tar xf /usr/lib/x86_64-linux-gnu/guestfs/supermin.d/init.tar.gz
/usr/bin/sed -i "s|^mdadm.*|mdadm --run /dev/md127|" init
/usr/bin/tar czvf init.tar.gz init
/usr/bin/mv init.tar.gz /usr/lib/x86_64-linux-gnu/guestfs/supermin.d/init.tar.gz
