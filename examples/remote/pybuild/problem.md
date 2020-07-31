# Read it and weep

- Namespace: cmgr/examples
- Type: remote-pybuild
- Category: Reverse Engineering
- Points: 100
- Templatable: yes
- MaxUsers: 0

## Description

We've found a mysterious binary lying around... Can you read it's secrets?

## Details

You can download the binary {{url_for("read_it", "here")}}.

You can connect to the service with `nc {{server}} {{port}}`.

## Hints

- There are two parts to the hidden message...

- A disassembler will help you make sense of the obfuscation

- A XOR B = C ::: A = C XOR B

## Solution Overview

This challenge consists of a binary containing obfuscated strings in the ASCII
printable set such that they appear when the binary is run through a strings
utility. The found strings then need to be analyzed and deobufscated to find the
components of the flag.

## Learning Objective

By the end of this challenge, competitors will learn how to identify static
strings within a binary and how to deobfuscate obfuscated strings.

## Solution Details

To solve this challenge, competitors must successfully identify and decode two
obfuscated strings that are within the challenge binary.

First, competitors must identify the target strings in question. There are two
possible approaches:

1. Use the Linux `strings` utility to dump all the binary's strings, and pick out the two
that look somewhat out of place.
2. Use a disassembler or debugger to find them by looking at the arguments to `memcmp()` within
the `main()` function.

Once competitors have the obfuscated strings, they will need to use a disassembler (such as
IDA, Ghidra, Binary Ninja, or similar) to reverse engineer two small obfuscation functions.
The competitors input will be run through these functions and compared to the obfuscated strings.

The first function performs a static XOR operation against each byte of user-input.
The second function builds a XOR key, then XOR's the key against user-input.

Competitors can re-implement these functions in a language of their choice, then run the
obfuscated strings through them to get the de-obfuscated versions. Alternatively, they
can use a debugger to call these functions straight from the original binary, but with the
obfuscated strings as input.

The first method (reimplementing the functions) is demonstrated in the provided solution
script (`solve.py`).

## Tags

- aacs4
- re
- grimm
- example

## Attributes

- event: aacs4
- organization: GRIMM
- time: 45m
- difficulty: easy
