GOLIB ?= golib

.PHONY: check ci inventory repository-check workflows

check:
	$(GOLIB) check --all

ci:
	$(GOLIB) repository check
	$(GOLIB) workflows check
	$(GOLIB) check --all

inventory repository-check:
	$(GOLIB) repository check

workflows:
	$(GOLIB) workflows check
