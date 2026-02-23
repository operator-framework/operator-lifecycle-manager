DESTINATION:=../

all:
	./gen-certs.sh

clean:
	rm -f *.csr *.crt *.key *.srl

copy:
	cp ca.crt $(DESTINATION)
	cp server-old.crt $(DESTINATION)
	cp server-old.key $(DESTINATION)
	cp server-new.crt $(DESTINATION)
	cp server-new.key $(DESTINATION)
