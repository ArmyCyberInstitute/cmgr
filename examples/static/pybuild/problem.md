# re101

- Namespace: cmgr/examples
- Type: static-pybuild
- Category: Reverse Engineering
- Points: 25
- Templatable: yes
- MaxUsers: 0

## Description

Reverse engineering can definitely be intimidating so we have a simple program with which you can start.

## Details

You can find the program {{url_for("re101", "here")}}. If you don't know where to start, check out the hints where we'll walk you through two different ways to solve this problem.

## Hints

- **Static analysis**:  Static analysis is a process for examining a program without having a computer execute any code.
    - From a command line on Linux, executing `objdump -d re101` will display the assembly code for the executable sections of the program (assumes you downloaded the file to the same folder).
    - Flow of this program starts at `_start` and proceeds 'down' the code.
    - `objdump -t re101` will print all of the 'symbols' in the program.  These are human-readable names for specific spots in memory.  Symbols in the '.text' section tend to be function names and symbols in '.bss', '.data', and '.rodata' tend to be variable names.
    - You should be able to see that the address of the 'flag' symbol (second command) appears in the first instruction of the '\_start' (first command).
    - The hex values that are moved look like they are in the printable ASCII range.
- **Dynamic analysis**:  Dynamic analysis is a method of examining the program as it is running to learn more about what it does.  A common tool to help with this is a \"debugger\" like `gdb`.
    1. `gdb re101` will launch GDB and prepare it to debug our target, the re101 executable.  You may need to change the permissions on the downloaded file in order to make it executable (`chmod a+x re101`).
    2. `break _exit` will add a \"breakpoint\" which will pause the program's execution when we reach this point in the code.  We're able to use '\_exit' here as a convenience and could have also specified a memory address instead.
    3. `run` will start execution and keep running until we hit the breakpoint we specified above.
    4. `x /s &flag` tells GDB to 'examine' a 'string' located at the address of the 'flag' symbol ('&' is the C symbol for 'address of').
- Instead of the dynamic analysis above, we could have also continued our static analysis by studying the assembly code we produced earlier.  In particular, we can observe that the code is moving a pointer to the 'flag' variable into the EDI register in the first line.  It then 'moves' a series of byte-constants into the memory location to which EDI points, 'incrementing' EDI in between each move.  The final three lines in '\_exit' execute a Linux system call to 'exit', but that is relevant for this problem.
- When doing reverse engineering of x86 and x86-64 programs, Intel's [instruction set reference](https://www.intel.com/content/dam/www/public/us/en/documents/manuals/64-ia-32-architectures-software-developer-instruction-set-reference-manual-325383.pdf) can be very helpful.  It can be intimidating to look at, but looking up the assembly instruction in this document will tell you exactly what it does.

## Learning Objective

By the end of this challenge, competitors should have a basic understanding of
how to disassemble a binary and use a debugger.

## Tags

- aacs4
- re
- example

## Attributes

- author: John Rollinson
- event: aacs4
- organization: Army Cyber Institute
