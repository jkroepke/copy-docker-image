/*
Copyright 2017 Matt Lavin <matt.lavin@gmail.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/genuinetools/reg/repoutils"
	"github.com/jkroepke/reg/registry"

	"github.com/alecthomas/kingpin"
	"github.com/sirupsen/logrus"

	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/schema2"
)

func migrateLayer(srcHub *registry.Registry, destHub *registry.Registry, srcRepo string, destRepo string, layer distribution.Descriptor) error {

	layerDigest := layer.Digest

	logrus.Debugf("Checking if manifest layer %s exists in destination registry", layerDigest)

	hasLayer, err := destHub.HasLayer(destRepo, layerDigest)
	if err != nil {
		return fmt.Errorf("Failure while checking if the destination registry contained an image layer. %v", err)
	}

	if hasLayer {
		logrus.Infof("Layer %s already exists in the destination", layerDigest)
		return nil
	}

	logrus.Infof("Upload layer %s to the destination", layerDigest)

	var wg sync.WaitGroup
	wg.Add(2)
	finished := make(chan bool, 1)
	errChannel := make(chan error, 1)

	read, write := io.Pipe()

	go func() {
		srcImageReader, err := srcHub.DownloadLayer(srcRepo, layerDigest)

		defer write.Close()
		defer srcImageReader.Close()

		if err != nil {
			errChannel <- fmt.Errorf("Failure while starting the download of an image layer. %v", err)
		}

		if _, err = io.Copy(write, srcImageReader); err != nil {
			errChannel <- fmt.Errorf("Failure while starting the download of an image layer. %v", err)
		}

		wg.Done()
	}()

	go func() {
		defer read.Close()

		err := destHub.UploadLayer(destRepo, layerDigest, read)

		if err != nil {
			errChannel <- fmt.Errorf("Failure while uploading the image. %v", err)
		}

		wg.Done()
	}()

	go func() {
		wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
	case err := <-errChannel:
		if err != nil {
			return err
		}
	}

	hasLayer, err = destHub.HasLayer(destRepo, layerDigest)
	if err != nil {
		return fmt.Errorf("Failure while checking if the destination registry contained an image layer. %v", err)
	}

	if !hasLayer {
		return fmt.Errorf("Can't find uploaded layer %s on target registry", layerDigest)
	}

	return nil
}

func copyImage(srcHub *registry.Registry, destHub *registry.Registry, srcArgs RepositoryArguments, destArgs RepositoryArguments) error {

	manifest, err := srcHub.ManifestV2(*srcArgs.Repository, *srcArgs.Tag)
	if err != nil {
		return fmt.Errorf("Failed to fetch the manifest for %s/%s:%s. %v", srcHub.URL, *srcArgs.Repository, *srcArgs.Tag, err)
	}

	deserializedManifest, err := schema2.FromStruct(manifest)
	if err != nil {
		return fmt.Errorf("Failed to deserialized the manifest for %s/%s:%s. %v", srcHub.URL, *srcArgs.Repository, *srcArgs.Tag, err)
	}

	resp, err := deserializedManifest.MarshalJSON()

	srcHub.Logf(string(resp))
	if manifest.Config.Digest == "" {
		return fmt.Errorf("Can't find config manifest for %s/%s:%s", srcHub.URL, *srcArgs.Repository, *srcArgs.Tag)
	}

	if len(manifest.Layers) == 0 {
		return fmt.Errorf("Can't find layer manifest for %s/%s:%s", srcHub.URL, *srcArgs.Repository, *srcArgs.Tag)
	}

	if manifest.SchemaVersion != 2 {
		return fmt.Errorf("Only SchemaVersion 2 is supported. Wrong manifest for %s/%s:%s", srcHub.URL, *srcArgs.Repository, *srcArgs.Tag)
	}

	var wg sync.WaitGroup
	wg.Add(len(manifest.Layers))
	finished := make(chan bool, 1)
	errChannel := make(chan error, 1)

	for _, layer := range manifest.Layers {
		go func(layer distribution.Descriptor) {
			logrus.Infof("Upload layer %s to the destination", layer.Digest)
			err := migrateLayer(srcHub, destHub, *srcArgs.Repository, *destArgs.Repository, layer)

			if err != nil {
				errChannel <- fmt.Errorf("Failed to migrate image layer. %v", err)
			}

			wg.Done()
		}(layer)
	}

	go func() {
		wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
	case err := <-errChannel:
		if err != nil {
			return err
		}
	}

	//Migrate config object first
	err = migrateLayer(srcHub, destHub, *srcArgs.Repository, *destArgs.Repository, manifest.Config)
	if err != nil {
		return fmt.Errorf("Failed to migrate config layer. %v", err)
	}

	err = destHub.PutManifest(*destArgs.Repository, *destArgs.Tag, deserializedManifest)
	if err != nil {
		return fmt.Errorf("Failed to upload manifest to %s/%s:%s. %v", destHub.URL, *destArgs.Repository, *destArgs.Tag, err)
	}

	logrus.Infof("Copied Docker Image %s:%s successfully.", *srcArgs.Repository, *srcArgs.Tag)
	return nil
}

type RepositoryArguments struct {
	RegistryURL  *string
	Repository   *string
	Tag          *string
	User         *string
	Password     *string
	Insecure     *bool
	ForceNoneSsl *bool
	SkipPing     *bool
}

func buildRegistryArguments(argPrefix string, argDescription string) RepositoryArguments {
	registryURLName := fmt.Sprintf("%s-url", argPrefix)
	registryURLDescription := fmt.Sprintf("URL of %s registry", argDescription)
	registryURLArg := kingpin.Flag(registryURLName, registryURLDescription).String()

	repositoryName := fmt.Sprintf("%s-repo", argPrefix)
	repositoryDescription := fmt.Sprintf("Name of the %s repository", argDescription)
	repositoryArg := kingpin.Flag(repositoryName, repositoryDescription).String()

	tagName := fmt.Sprintf("%s-tag", argPrefix)
	tagDescription := fmt.Sprintf("Name of the %s tag", argDescription)
	tagArg := kingpin.Flag(tagName, tagDescription).String()

	userName := fmt.Sprintf("%s-user", argPrefix)
	userDescription := fmt.Sprintf("Name of the %s user", argDescription)
	userArg := kingpin.Flag(userName, userDescription).String()

	passwordName := fmt.Sprintf("%s-password", argPrefix)
	passwordDescription := fmt.Sprintf("Password for %s", argDescription)
	passwordArg := kingpin.Flag(passwordName, passwordDescription).String()

	insecureName := fmt.Sprintf("%s-insecure", argPrefix)
	insecureDescription := fmt.Sprintf("Do not verify tls certificates for %s", argDescription)
	insecureArg := kingpin.Flag(insecureName, insecureDescription).Bool()

	forceNonSslName := fmt.Sprintf("%s-force-non-ssl", argPrefix)
	forceNonSslDescription := fmt.Sprintf("force allow use of non-ssl for %s", argDescription)
	forceNonSslArg := kingpin.Flag(forceNonSslName, forceNonSslDescription).Bool()

	skipPingName := fmt.Sprintf("%s-skip-ping", argPrefix)
	skipPingDescription := fmt.Sprintf("skip pinging the registry while establishing connection for %s", argDescription)
	skipPingArg := kingpin.Flag(skipPingName, skipPingDescription).Bool()

	return RepositoryArguments{
		RegistryURL:  registryURLArg,
		Repository:   repositoryArg,
		Tag:          tagArg,
		User:         userArg,
		Password:     passwordArg,
		Insecure:     insecureArg,
		ForceNoneSsl: forceNonSslArg,
		SkipPing:     skipPingArg,
	}
}

func connectToRegistry(args RepositoryArguments, debugArg *bool, timeoutArg time.Duration) (*registry.Registry, error) {

	url := *args.RegistryURL

	username := ""
	password := ""

	if args.User != nil {
		username = *args.User
	}
	if args.Password != nil {
		password = *args.Password
	}

	auth, err := repoutils.GetAuthConfig(username, password, *args.RegistryURL)

	registry, err := registry.New(auth, registry.Opt{
		Insecure: *args.Insecure,
		Debug:    *debugArg,
		SkipPing: *args.SkipPing,
		Timeout:  timeoutArg,
		Headers: map[string]string{
			"User-Agent": "docker/master copy-docker-image/2",
		},
	})

	if err != nil {
		return nil, fmt.Errorf("Failed to create registry connection for %s. %v", url, err)
	}

	return registry, nil
}

func main() {
	exitCode := 0
	defer func() {
		os.Exit(exitCode)
	}()

	srcArgs := buildRegistryArguments("src", "source")
	destArgs := buildRegistryArguments("dest", "destination")
	repoArg := kingpin.Flag("repo", "The repository in the source and the destination. Values provided by --srcRepo or --destTag will override this value").String()
	tagArg := kingpin.Flag("tag", "The tag name in the source and the destination. Values provided by --srcTag or --destTag will override this value").Default("latest").String()
	debugArg := kingpin.Flag("debug", "Enable debug mode.").Bool()
	timeoutArg := kingpin.Flag("timeout", "Timeout for registry actions").Default("1m").String()
	kingpin.Parse()

	timeout, err := time.ParseDuration(*timeoutArg)
	if err != nil {
		logrus.Errorf("parsing %s as duration failed: %v", timeout, err)
		exitCode = -1
		return
	}

	if *srcArgs.Repository == "" {
		srcArgs.Repository = repoArg
	}
	if *destArgs.Repository == "" {
		destArgs.Repository = repoArg
	}

	if *srcArgs.Tag == "" {
		srcArgs.Tag = tagArg
	}
	if *destArgs.Tag == "" {
		destArgs.Tag = tagArg
	}

	if *srcArgs.Repository == "" {
		logrus.Errorf("A source repository name is required either with --src-repo or --repo")
		exitCode = -1
		return
	}

	if *destArgs.Repository == "" {
		logrus.Errorf("A destination repository name is required either with --dest-repo or --repo")
		exitCode = -1
		return
	}

	srcHub, err := connectToRegistry(srcArgs, debugArg, timeout)
	if err != nil {
		logrus.Errorf("Failed to establish a connection to the source registry. %v", err)
		exitCode = -1
		return
	}

	destHub, err := connectToRegistry(destArgs, debugArg, timeout)
	if err != nil {
		logrus.Errorf("Failed to establish a connection to the destination registry. %v", err)
		exitCode = -1
		return
	}

	err = copyImage(srcHub, destHub, srcArgs, destArgs)
	if err != nil {
		logrus.Errorf("An error occured: %s", err)
		exitCode = -1
		return
	}

}
