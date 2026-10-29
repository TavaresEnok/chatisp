.PHONY: test lint generate dev

# Cada alvo só chama o script equivalente. Sem make, execute scripts/ diretamente.

test:
	./scripts/test.sh

lint:
	./scripts/lint.sh

generate:
	./scripts/generate.sh

dev:
	./scripts/dev.sh
