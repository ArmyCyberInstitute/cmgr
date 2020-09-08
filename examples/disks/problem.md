# Recovery And IDentification

- Namespace: cmgr/examples
- Type: static-make
- Category: Miscellaneous
- Points: 150
- Templatable: Yes
- MaxUsers: 0

## Description

Given two out of three disk images used in a RAID array, can you can recover
the data?

## Details

A tar archive of two of the disks is available {{url_for('disks.tar.gz', 'here')}}.

## Hints

- [Creating your own RAID array](https://unix.stackexchange.com/questions/302766/persistent-use-of-loop-block-device-in-mdadm) would be a good place to start.

- Try to [use your existing array to assemble a RAID array with the given disk images](https://superuser.com/questions/962395/assemble-3-drive-software-raid5-with-one-disk-missing).

## Solution Overview

This challenge consists of a corrupted set of disk images that were originally
set up using a RAID configuration. Competitors will have to recover the data
from the corrupted disk in order to find the flag.

## Learning Objective

By the end of this challenge, competitors will have learned about RAID
configurations and low-level file configurations, and they will have gained
experience performing disk recovery.

## Attributes

- organization: GRIMM
- version: 2.0.0
- event: aacs4
