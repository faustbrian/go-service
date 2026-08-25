SHELL := /usr/bin/env bash

.PHONY: check ci clean-consumer integration-compatibility interoperability \
	inventory repository-check

check:
	./.golib/scripts/with-disposable-go-cache.sh ./.golib/scripts/run-modules.sh check --all

ci: repository-check check

integration-compatibility:
	./.golib/scripts/with-disposable-go-cache.sh \
		./.golib/scripts/check-module.sh compatibility tidy-check
	./.golib/scripts/with-disposable-go-cache.sh \
		./.golib/scripts/check-module.sh compatibility race
	./.golib/scripts/with-disposable-go-cache.sh \
		./.golib/scripts/check-module.sh compatibility vulnerability

clean-consumer:
	./.golib/scripts/with-disposable-go-cache.sh \
		./scripts/check-clean-consumer.sh

interoperability: integration-compatibility clean-consumer

inventory repository-check:
	./.golib/scripts/repository-check.sh
