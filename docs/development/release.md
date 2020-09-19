** This file documents the release process used through kops 1.18.
For the new process that will be used for 1.19, please see
[the new release process](../release-process.md)**

# Release Process

The kops project is released on an as-needed basis. The process is as follows:

1. An issue is proposing a new release with a changelog since the last release
1. All [OWNERS](https://github.com/kubernetes/kops/blob/master/OWNERS) must LGTM this release
1. An OWNER runs `git tag -s $VERSION` and inserts the changelog and pushes the tag with `git push $VERSION`
1. The release issue is closed
1. An announcement email is sent to `kubernetes-dev@googlegroups.com` with the subject `[ANNOUNCE] kops $VERSION is released`

## Branches

We maintain a `release-1.16` branch for kops 1.16.X, `release-1.17` for kops 1.17.X
etc.

`master` is where development happens.  We create new branches from master as a
new kops version is released, or in preparation for a new release.  As we are
preparing for a new kubernetes release, we will try to advance the master branch
to focus on the new functionality, and start cherry-picking back more selectively
to the release branches only as needed.

Generally we don't encourage users to run older kops versions, or older
branches, because newer versions of kops should remain compatible with older
versions of Kubernetes.

Releases should be done from the `release-1.X` branch.  The tags should be made
on the release branches.

We do currently maintain a `release` branch which should point to the same tag as
the current `release-1.X` tag.

## New Kubernetes versions and release branches

Typically Kops alpha releases are created off the master branch and beta and stable releases are created off of release branches.
In order to create a new release branch off of master prior to a beta release, perform the following steps:

1. Create a new periodic E2E prow job for the "next" kubernetes minor version.
   * All Kops prow jobs are defined [here](https://github.com/kubernetes/test-infra/tree/master/config/jobs/kubernetes/kops)
2. Create a new presubmit E2E prow job for the new release branch.
3. Create a new milestone in the GitHub repo.
4. Update [prow's milestone_applier config](https://github.com/kubernetes/test-infra/blob/dc99617c881805981b85189da232d29747f87004/config/prow/plugins.yaml#L309-L313) to update master to use the new milestone and add an entry for the new branch that targets master's old milestone.
5. Create the new release branch in git and push it to the GitHub repo.

## Update versions

See [1.5.0-alpha4 commit](https://github.com/kubernetes/kops/commit/a60d7982e04c273139674edebcb03c9608ba26a0) for example

* Use the hack/set-version script to update versions:  `hack/set-version 1.20.0 1.20.1`

The syntax is `hack/set-version <new-release-version> <new-ci-version>`

`new-release-version` is the version you are releasing.

`new-ci-version` is the version you are releasing "plus one"; this is used to avoid CI jobs being out of semver order.

Examples:

| new-release-version  | new-ci-version
| ---------------------| ---------------
| 1.20.1               | 1.20.2
| 1.21.0-alpha.1       | 1.21.0-alpha.2
| 1.21.0-beta.1        | 1.21.0-beta.2


* Update the golden tests: `hack/update-expected.sh`

* Commit the changes (without pushing yet): `git commit -m "Release 1.X.Y"`

## Check builds OK

```
rm -rf .build/ .bazelbuild/
make ci
```


## Push new kops-controller / dns-controller images

```
# For versions prior to 1.18: make dns-controller-push DOCKER_REGISTRY=kope
make dns-controller-push DOCKER_IMAGE_PREFIX=kope/  DOCKER_REGISTRY=index.docker.io

make kube-apiserver-healthcheck-push DOCKER_IMAGE_PREFIX=kope/  DOCKER_REGISTRY=index.docker.io

make kops-controller-push DOCKER_IMAGE_PREFIX=kope/  DOCKER_REGISTRY=index.docker.io
```

## Upload new version

```
# export AWS_PROFILE=??? # If needed
make upload UPLOAD_DEST=s3://kubeupv2
```

## Tag new version

Make sure you are on the release branch `git checkout release-1.X`

```
make release-tag
git push git@github.com:kubernetes/kops
git push --tags git@github.com:kubernetes/kops

# Sync the origin alias back up
git fetch origin
```

## Update release branch

For the time being, we are also maintaining a release branch.  We push released
versions to that.

`git push git@github.com:kubernetes/kops release-1.17:release`

## Pull request to master branch (for release commit)

## Upload to github

Use [shipbot](https://github.com/kopeio/shipbot) to upload the release:

```
make release-github
```


## Upload to new k8s artifacts locations (k8s.gcr.io / artifacts.k8s.io)

Assuming that you have `https://github.com/kubernetes/k8s.io` checked out at ~/k8s/src/k8s.io/k8s.io

Make sure `VERSION` is set to the current version:
```
VERSION=$(tools/get_workspace_status.sh | grep KOPS_VERSION | awk '{print $2}')
echo ${VERSION}
```

```
gsutil rsync -r s3://kubeupv2/kops/${VERSION}/ gs://k8s-staging-kops/kops/releases/${VERSION}/

cd ~/k8s/src/k8s.io/k8s.io

mkdir -p ./k8s-staging-kops/kops/releases/${VERSION}/

gsutil rsync -r gs://k8s-staging-kops/kops/releases/${VERSION}/ ./k8s-staging-kops/kops/releases/${VERSION}/

promobot-generate-manifest --src ~/k8s/src/k8s.io/k8s.io/k8s-staging-kops/kops/releases/ --prefix ${VERSION}  > ~/k8s/src/k8s.io/k8s.io/artifacts/manifests/k8s-staging-kops/${VERSION}.yaml
```

```
crane cp kope/kube-apiserver-healthcheck:${VERSION} gcr.io/k8s-staging-kops/kube-apiserver-healthcheck:${VERSION}
crane cp kope/dns-controller:${VERSION} gcr.io/k8s-staging-kops/dns-controller:${VERSION}
crane cp kope/kops-controller:${VERSION} gcr.io/k8s-staging-kops/kops-controller:${VERSION}

cd ~/k8s/src/k8s.io/k8s.io/k8s.gcr.io/images/k8s-staging-kops
echo "" >> images.yaml
echo "# ${VERSION}" >> images.yaml
k8s-container-image-promoter --snapshot gcr.io/k8s-staging-kops --snapshot-tag ${VERSION} >> images.yaml
```

```
# Send PR
cd ~/k8s/src/k8s.io/k8s.io
git add k8s.gcr.io/images/k8s-staging-kops/images.yaml
git add artifacts/manifests/k8s-staging-kops/${VERSION}.yaml
git commit -m "Promote artifacts for kops ${VERSION}"
git push ${USER}
hub pull-request
```

You will need to follow the manual binary promoter process from [the new release process](../release-process.md) until that step is automated.


## Compile release notes

e.g.

```
FROM=1.14.0
TO=1.14.1
DOC=1.14
git log ${FROM}..${TO} --oneline | grep Merge.pull | grep -v Revert..Merge.pull | cut -f 5 -d ' ' | tac  > /tmp/prs
echo -e "\n## ${FROM} to ${TO}\n"  >> docs/releases/${DOC}-NOTES.md
relnotes  -config .shipbot.yaml  < /tmp/prs  >> docs/releases/${DOC}-NOTES.md
```

## On github

* Download release
* Validate it
* Add notes
* Publish it

## Release kops to homebrew

* Following the [documentation](homebrew.md) we must release a compatible homebrew formulae with the release.
* This should be done at the same time as the release, and we will iterate on how to improve timing of this.

## Update the alpha channel and/or stable channel

Once we are satisfied the release is sound:

* Bump the kops recommended version in the alpha channel

Once we are satisfied the release is stable:

* Bump the kops recommended version in the stable channel

## Update conformance results with CNCF

Use the following instructions: https://github.com/cncf/k8s-conformance/blob/master/instructions.md

