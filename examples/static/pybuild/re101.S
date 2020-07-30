.text
.global _start
.global _exit

_start:
    lea flag, %edi
{% for c in flag %}
    movb $'{{c}}', (%edi)
    inc %edi
{% endfor %}
    movb $0, (%edi)

_exit:
    movb $1, %al  /* Syscall number */
    movl $0, %ebx /* Exit code */
    int $0x80     /* Make syscall */

.bss
.global flag
flag:
  .space 128
