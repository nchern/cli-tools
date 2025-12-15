

install-all:
	find . -mindepth 2 -type f -name Makefile -execdir make install \;
