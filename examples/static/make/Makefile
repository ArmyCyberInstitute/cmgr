.PHONY: main
main: lockbox

FLAG_PREFIX := $(shell echo $(FLAG_FORMAT) | sed 's/^\([a-zA-Z0-9]*\).*/\1/')

artifacts.tar.gz: lockbox
	tar czvf $@ $^

lockbox: lockbox.c
	gcc -Wall -Werror -D"FLAG_PREFIX=\"$(FLAG_PREFIX)\"" -o $@ $^ -lcrypto

metadata.json:
	echo "{\"flag\":\"$(FLAG_PREFIX){c0de_has_mil_grade_crypto}\"}" > metadata.json

clean:
	rm -f main lockbox
