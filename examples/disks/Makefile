.PHONY: main
main: disks.tar.gz

disks.tar.gz: disk0.img disk2.img
	tar czf $@ $^

artifacts.tar.gz: disks.tar.gz
	tar czf $@ $^

flag:
	echo -n "$(FLAG)" > $@

# Random noise to ensure the parity block for the flag is not a trivial copy.
a.ignore:
	dd if=/dev/urandom of=$@ bs=4096 count=128

disk0.img disk1.img disk2.img: create_disks.gfsh a.ignore flag
	guestfish -f $<

metadata.json:
	echo -n "{\"flag\":\"$(FLAG)\"}" > metadata.json

.PHONY: clean
clean:
	rm -f a.ignore
	rm -f artifacts.tar.gz
	rm -f disk*
	rm -f flag
	rm -f metadata.json

