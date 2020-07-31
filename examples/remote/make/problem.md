# BinEx101

- Namespace: cmgr/examples
- Type: remote-make
- Category: Binary Exploitation
- Points: 50
- Templatable: yes
- MaxUsers: 0

## Description

Exploiting bugs in programs can definitely be difficult.  Not only do you need
a certain amount of reverse engineering required to identify vulnerabilities,
but you also need to weaponize that vulnerability somehow.  This challenge is
designed to get new hackers started and familiar with C.

## Details
We've made our annotated {{url_for('BinEx101.c', 'source code')}} along with the compiled {{url_for('BinEx101', 'program')}} available for download.

If you don't know where to start, download the source code and open it in a program with syntax highlighting such as `notepad++` or `gedit`.  If you don't have the ability to use either of those, you can always use `vim`.

You can connect to the problem at `telnet {{server}} {{port}}` or `nc {{server}} {{port}}`

## Hints

- Signed integers on modern computers generally use something called ["Two's Complement"](https://en.wikipedia.org/wiki/Two%27s_complement) for representing them.  If this is your first time dealing with integers at this level, it is probably worth taking some time to get a basic understanding of them.  In particular, you will need to understand what the largest positive number looks like, what -1 looks like, and how [overflow](https://en.wikipedia.org/wiki/Integer_overflow) is generally "handled".

- We've also included debug symbols in the binary and disabled compiler optimizations.  Once you understand how the C code works from the source code, it is probably worth opening the compiled binary in something like [Ghidra](https://ghidra-sre.org/) to see both what the assembly looks like and how the recovered C code compares to the source code.  Most of the other binary exploitation problems do not give you access to the raw source code.

- While many binary exploitation situations involve "non-standard" inputs (such as feeding shellcode as input to the name of something), this challenge does not.  Once you understand the vulnerability, you can trigger it through normal interaction with the challenge.  If you are having trouble on the math side, treating the binary representation of your 'target' number as an unsigned integer may be helpful.

- If you are new to binary exploitation (or C code), we really recommend reading the source file in its entirety as the comments try to explain many of the key concepts for this category of problems.  For this specific problem, anyone not familiar C should definitely read the source file because the behavior of `s.numbers[-1]` is _very_ different between C and some other popular languages (e.g. Python).

## Solution Overview

This challenge consists of a simple program that multiplies two user-provided
numbers and then uses it to (unsafely) access an array.  The competitor has
access to well-documented source code that directly describes the concepts
needed to solve the problem such as how arrays work in C and how integer
overflow can result in serious code vulnerabilities.  The competitor only
needs to enter two positive integer values that, after overflow, result in an
array access of -1.

## Learning Objective

By the end of this challenge, competitors should have a basic idea of how to
approach future binary exploitation problems as well as the ability to read C
code and understand integer overflows.

## Tags

- aacs4
- binex
- example

## Attributes

- author: John Rollinson
- event: aacs4
- organization: Army Cyber Institute
