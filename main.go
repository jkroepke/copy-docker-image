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
	"os"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/jkroepke/reg/registry"
	"github.com/genuinetools/reg/repoutils"

	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/schema2"
)

func moveLayerUsingFile(srcHub *registry.Registry, destHub *registry.Registry, srcRepo string, destRepo string, layer distribution.Descriptor) error {

	layerDigest := layer.Digest

	srcImageReader, err := srcHub.DownloadLayer(srcRepo, layerDigest)
	if err != nil {
		return fmt.Errorf("Failure while starting the download of an image layer. %v", err)
	}

	err = destHub.UploadLayer(destRepo, layerDigest, srcImageReader)
	if err != nil {
		return fmt.Errorf("Failure while uploading the image. %v", err)
	}

	return nil
}


func migrateLayer(srcHub *registry.Registry, destHub *registry.Registry, srcRepo string, destRepo string, layer distribution.Descriptor) error {

	srcHub.Logf("Checking if manifest layer exists in destination registry")

	layerDigest := layer.Digest
	hasLayer, err := destHub.HasLayer(destRepo, layerDigest)
	if err != nil {
		return fmt.Errorf("Failure while checking if the destination registry contained an image layer. %v", err)
	}

	if !hasLayer {
		fmt.Println("Need to upload layer", layerDigest, "to the destination")
		err = moveLayerUsingFile(srcHub, destHub, srcRepo, destRepo, layer)
		return err
	}

	srcHub.Logf("Layer already exists in the destination")
	return nil

}

func copyImage(srcHub *registry.Registry, destHub *registry.Registry, srcArgs RepositoryArguments, destArgs RepositoryArguments) (int, error) {

	manifest, err := srcHub.ManifestV2(*srcArgs.Repository, *srcArgs.Tag)
	if err != nil {
		return -1, fmt.Errorf("Failed to fetch the manifest for %s/%s:%s. %v", srcHub.URL, *srcArgs.Repository, *srcArgs.Tag, err)
	}

	deserializedManifest, err := schema2.FromStruct(manifest)
	if err != nil {
		return -1, fmt.Errorf("Failed to deserialized the manifest for %s/%s:%s. %v", srcHub.URL, *srcArgs.Repository, *srcArgs.Tag, err)
	}

	resp, err := deserializedManifest.MarshalJSON()

	srcHub.Logf(string(resp))

	//Migrate config object first
	err = migrateLayer(srcHub, destHub, *srcArgs.Repository, *destArgs.Repository, manifest.Config)
	if err != nil {
		return -1, fmt.Errorf("Failed to migrate image layer. %v", err)
	}

	for _, layer := range manifest.Layers {
		srcHub.Logf("Uploading Layer: %s", layer.Digest)
		err := migrateLayer(srcHub, destHub, *srcArgs.Repository, *destArgs.Repository, layer)
		if err != nil {
			return -1, fmt.Errorf("Failed to migrate image layer. %v", err)
		}
	}

	err = destHub.PutManifest(*destArgs.Repository, *destArgs.Tag, deserializedManifest)
	if err != nil {
		return -1, fmt.Errorf("Failed to upload manifest to %s/%s:%s. %v", destHub.URL, *destArgs.Repository, *destArgs.Tag, err)
	}

	fmt.Printf("Copied Docker Image %s:%s successfully.\n", *srcArgs.Repository, *srcArgs.Tag)
	return 0, nil
}

type RepositoryArguments struct {
	RegistryURL  *string
	Repository   *string
	Tag          *string
	User         *string
	Password     *string
	Timeout      *string
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

	timeoutName := fmt.Sprintf("%s-timeout", argPrefix)
	timeoutDescription := fmt.Sprintf("Timeout for %s", argDescription)
	timeoutArg := kingpin.Flag(timeoutName, timeoutDescription).Default("1m").String()

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
		RegistryURL: 	registryURLArg,
		Repository:  	repositoryArg,
		Tag:         	tagArg,
		User:        	userArg,
		Password:    	passwordArg,
		Insecure:    	insecureArg,
		ForceNoneSsl:   forceNonSslArg,
		SkipPing:    	skipPingArg,
		Timeout:    	timeoutArg,
	}
}

func connectToRegistry(args RepositoryArguments, debugArg *bool) (*registry.Registry, error) {

	url := *args.RegistryURL

	username := ""
	password := ""

	if args.User != nil {
		username = *args.User
	}
	if args.Password != nil {
		password = *args.Password
	}


	timeout, err := time.ParseDuration(*args.Timeout)
	if err != nil {
		return nil, fmt.Errorf("parsing %s as duration failed: %v", timeout, err)
	}

	auth, err := repoutils.GetAuthConfig(username, password, *args.RegistryURL)

	registry, err := registry.New(auth, registry.Opt{
		Insecure: *args.Insecure,
		Debug:    *debugArg,
		SkipPing: *args.SkipPing,
		Timeout:  timeout,
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
	destArgs := buildRegistryArguments("dest", "destiation")
	repoArg := kingpin.Flag("repo", "The repository in the source and the destiation. Values provided by --srcRepo or --destTag will override this value").String()
	tagArg := kingpin.Flag("tag", "The tag name in the source and the destiation. Values provided by --srcTag or --destTag will override this value").Default("latest").String()
	debugArg := kingpin.Flag("debug", "Enable debug mode.").Bool()
	kingpin.Parse()

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
		fmt.Printf("A source repository name is required either with --srcRepo or --repo\n")
		exitCode = -1
		return
	}

	if *destArgs.Repository == "" {
		fmt.Printf("A destiation repository name is required either with --destRepo or --repo\n")
		exitCode = -1
		return
	}

	srcHub, err := connectToRegistry(srcArgs, debugArg)
	if err != nil {
		fmt.Printf("Failed to establish a connection to the source registry. %v\n", err)
		exitCode = -1
		return
	}

	destHub, err := connectToRegistry(destArgs, debugArg)
	if err != nil {
		fmt.Printf("Failed to establish a connection to the destination registry. %v\n", err)
		exitCode = -1
		return
	}

	_, err = copyImage(srcHub, destHub, srcArgs, destArgs)
	if err != nil {
		fmt.Printf("An error occured: %s\n", err)
		exitCode = -1
		return
	}

}
