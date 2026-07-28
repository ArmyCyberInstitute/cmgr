/*
 * Adapted from picoCTF hacksport release v19.2.10, commit
 * fb09fa2cb745c2db007dc4be8f95e37a1788c830.
 * Copyright (c) 2013-2016 Carnegie Mellon University; MIT licensed.
 * See LICENSE.picoCTF.
 */
#include <stdlib.h>
#include <sys/personality.h>
#include <unistd.h>

extern char **environ;

int main(int argc, char **argv) {
    (void)argc;
    if (personality(ADDR_NO_RANDOMIZE) == -1) {
        return EXIT_FAILURE;
    }
    argv[0] = BINARY_PATH;
    execve(BINARY_PATH, argv, environ);
    return EXIT_FAILURE;
}
