# apt-repo-builder

CURRENTLY UNDER TEST

**apt-repo-builder** is a stateless tool that turns GitHub Releases into fully functional APT repositories (Debian/Ubuntu).

It turns .deb file uploaded to GitHub Releases, into a valid APT Repository for Debian/Ubuntu to get automated updates.

## Configuration

Create a `apt-repo-builder.yaml` file to create a project repository.


```yaml
project:
  archive_info:
    origin: "MyRepo"
    label: "my-tools"
    suite: "stable"
    codename: "stable"
    architectures: "amd64 arm64"
    components: "main"
    description: "My Custom APT Repository"
  sources:
    - "github.com/my-org/my-tool"
    - "github.com/cli/cli"

upstream:
  sources:
    - url: "http://archive.ubuntu.com/ubuntu"
      suite: "focal"
      component: "main"
      architectures: ["amd64"]
```

`$ apt-repo-builder index --to github.com/my-org/my-repo/tags/stable`

It will upload all the Debian standard index files to the "stable" Release tag, and
you can ask your users to register your APT repo to get the updates from your tools.

`deb [signed-by=/etc/apt/keyrings/my-repo.gpg] https://github.com/my-org/my-repo/releases/download/stable/ ./`

is the entry to add to any Debian based system.

**apt-repo-builder** comes with a Github Action to automate your build process and to make sure that new .deb can be added to your repository, and optionally will upload them to github.


See Documentation for all the details.
