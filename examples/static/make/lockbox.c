#include <openssl/sha.h>
#include <stdio.h>
#include <string.h>
#include <stdint.h>

#define BUFFER_SIZE 64

// ACI{c0de_has_mil_grade_crypto}
static const char flag_format_string[] = "%s: %s\x7b%x_%s_%s_%s_%s\x7d\n";
static const char header[] = "flag";
static const char prefix[] = FLAG_PREFIX;
static const uint32_t code = 0xc0de;
static const char uses[] = "has";
static const char military[] = "mil";
static const char grade[] = "grade";
static const char crypto[] = "crypto";

// SHA256("correct horse battery staple")
static const uint8_t password_hash[SHA256_DIGEST_LENGTH] = {
    0xc4, 0xbb, 0xcb, 0x1f, 0xbe, 0xc9, 0x9d, 0x65,
    0xbf, 0x59, 0xd8, 0x5c, 0x8c, 0xb6, 0x2e, 0xe2,
    0xdb, 0x96, 0x3f, 0x0f, 0xe1, 0x06, 0xf4, 0x83,
    0xd9, 0xaf, 0xa7, 0x3b, 0xd4, 0xe3, 0x9a, 0x8a
};

int main(int argc, char **argv) {
    printf("Enter the password to get the flag: ");

    char buf[BUFFER_SIZE];
    fgets(buf, BUFFER_SIZE, stdin);
    int len = strlen(buf);
    if (buf[len-1] == '\n') {
        buf[--len] = 0;
    }

    uint8_t hash[SHA256_DIGEST_LENGTH];
    SHA256_CTX hash_ctx;
    SHA256_Init(&hash_ctx);
    SHA256_Update(&hash_ctx, buf, len);
    SHA256_Final(hash, &hash_ctx);

    if (memcmp(hash, password_hash, SHA256_DIGEST_LENGTH)) {
        puts("Wrong password so no flag for you!");
        return -1;
    }

    printf(flag_format_string, header, prefix, code, uses, military, grade, crypto);
    return 0;
}
