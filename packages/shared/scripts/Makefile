.PHONY: local-build-base-template
local-build-base-template:
	echo "Building base template for local dev..."
	npm install
	DOTENV_CONFIG_PATH=./.env.local \
		npx tsx \
			--import dotenv/config \
			build.prod.ts
