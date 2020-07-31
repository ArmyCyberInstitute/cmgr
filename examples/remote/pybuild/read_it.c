// gcc ./read_it.c -o read_it
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

char secret1[17] = "{{secret1}}";
char secret2[17] = "{{secret2}}";
char key[17] = "{{key}}";

char * encode_first(char * data) {
    char * ret = malloc(16);
    unsigned int i;

    memset(ret, 0, 16);

    for (i=0; i < 16; i++) {
        ret[i] = (data[i] ^ 0x17);
    }

    return ret;
}

char * encode_second(char * data) {
    unsigned int i;

    for(i=0; i<16; i++) {
        key[i] = (key[i] >> 4) % 0x7f;
    }

    char * ret = malloc(16);
    memset(ret, 0, 16);

    for(i=0; i<16; i++) {
        ret[i] = (data[i] ^ key[i]);
    }

    return ret;
}

void read_and_print_flag() {
    char flag_data[64];
    FILE * fp = fopen("./flag", "r");

    fgets(flag_data, 64, fp);
    fclose(fp);

    printf("%s", flag_data);
}

void main() {
	char user_guess[64];

	printf("There's a hidden message in this binary\n");
	printf("Find it, and get a flag!\n");
    printf(">>> ");
    fflush(0);


	fgets((char *)&user_guess, 64, stdin);

    char * part1 = encode_first(user_guess);
    char * part2 = encode_second(&user_guess[16]);

    if (memcmp(part1, secret1, 16) != 0) {
        printf("Sorry, that's not correct!\n");
        fflush(0);
        return;
    }

    if (memcmp(part2, secret2, 16) != 0) {
        printf("Sorry, that's not correct!\n");
        fflush(0);
        return;
    }

    printf("Correct! Here is your flag:\n");
    read_and_print_flag();

    return;
}
