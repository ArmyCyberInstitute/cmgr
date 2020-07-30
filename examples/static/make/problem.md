# Lockbox

- Namespace: cmgr/examples
- Type: static-make
- Category: Reverse Engineering
- Points: 50
- Templatable: yes
- MaxUsers: 0

## Description

We developed a password-protected lockbox which uses a super-secure, military-grade hash function with 256-bits of security to ensure only someone with the proper password can print the flag.

## Details
You can download the file from {{url_for("lockbox", "here")}}.

## Hints

- You do not need to crack the password.
- Tools like [ghidra](https://ghidra-sre.org/) are helpful when `strings` isn't enough.
- Looking at calls to `printf` and `puts` is probably a good place to start.

## Solution Overview

This challenge consists of binary that uses a hashing algorithm to protect a
password.  However, the flag can be extracted by reversing the print function
at the end of the main routine.  Alternatively, competitors can patch the
binary to skip the password check.

## Learning Objective

By the end of this challenge, competitors should be able to navigate a binary
by looking at external function calls.

## Tags

- aacs4
- crypto
- re
- example

## Attributes

- author: John Rollinson
- event: aacs4
- organization: Army Cyber Institute
