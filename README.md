# move-docker-image - Copy docker images without a full docker installation
[![Build Status](https://travis-ci.org/jkroepke/copy-docker-image.svg?branch=master)](https://travis-ci.org/jkroepke/copy-docker-image)

## Overview

When doing automated deployments, especially when using AWS ECR in multiple accounts, you might want to copy images from one registry to another without the need for a full docker installation. At LifeOmic we wanted to orchestrate the copying of images while executing inside a container without exposing a full Docker socker just for image manipulation.

To copy an image between two anonymous repositories, you can use a command line like:

```
$ copy-docker-image --src-repo http://registry1/ --dest-repo http://registry2 --repo project
```

To specify an image tag, just add a --tag argument like:

```
$ copy-docker-image --src-repo http://registry1/ --dest-repo http://registry2 --repo project --tag v1
```

To get an image from a private registry

```
$ copy-docker-image --src-repo http://registry1/ --src-user Username --src-password Password --dest-repo http://registry2 --repo project --tag v1
```

## Installation

Pre-built binaries for tagged releases are available on the [releases page](https://github.com/jkroepke/copy-docker-image/releases).
