The provision.sh script file sets up any base dependencies required for running envd and sdk commands.

# change rollout process

The provision.sh script is executed immediately after the docker image is pulled from the registry, and before running
any other build commands, and the result is cached. As such, it is most likely cached for any future builds. The caching
means that the provision.sh script will most likely not be executed if the template is rebuilt.

There are two considerations to be made when rolling out new versions of the template:
- We don't want to overwhelm the template managers by invalidating all cache at once.
- We want to communicate to the end users that, in order to take advantage of new features, customers need to rebuild their templates.

Current process:

1. Deploy template managers with the new version of `provision.sh`.
2. Increment the `build-provision-version` launch darkly flag.
3. Bump the envd version number and release it. Gate the release of new envd/sdk features with the new envd version.
4. (optional) Update the dashboard to alert users to rebuild their templates to take advantage of the new features.
