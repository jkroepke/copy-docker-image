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
	"io/ioutil"
	"os"
	"strings"

	"github.com/alecthomas/kingpin"
	"github.com/docker/distribution"
	"github.com/fsouza/go-dockerclient"
	"github.com/heroku/docker-registry-client/registry"
)

func moveLayerUsingFile(srcHub *registry.Registry, destHub *registry.Registry, srcRepo string, destRepo string, layer distribution.Descriptor, file *os.File) error {

	layerDigest := layer.Digest

	srcImageReader, err := srcHub.DownloadLayer(srcRepo, layerDigest)
	if err != nil {
		return fmt.Errorf("Failure while starting the download of an image layer. %v", err)
	}

	_, err = io.Copy(file, srcImageReader)
	if err != nil {
		return fmt.Errorf("Failure while copying the image layer to a temp file. %v", err)
	}
	srcImageReader.Close()
	file.Sync()

	imageReadStream, err := os.Open(file.Name())
	if err != nil {
		return fmt.Errorf("Failed to open the temporary image layer for uploading. %v", err)
	}
	err = destHub.UploadLayer(destRepo, layerDigest, imageReadStream)
	imageReadStream.Close()
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
		tempFile, err := ioutil.TempFile("", "docker-image")
		if err != nil {
			return fmt.Errorf("Failure while a creating temporary file for an image layer download. %v", err)
		}

		err = moveLayerUsingFile(srcHub, destHub, srcRepo, destRepo, layer, tempFile)
		removeErr := os.Remove(tempFile.Name())
		if removeErr != nil {
			// Print the error but don't fail the whole migration just because of a leaked temp file
			fmt.Printf("Failed to remove image layer temp file %s. %v", tempFile.Name(), removeErr)
		}

		return err
	}

	srcHub.Logf("Layer already exists in the destination")
	return nil

}

func copyImage(srcHub *registry.Registry, destHub *registry.Registry, repository string, tag string) (int, error) {

	manifest, err := srcHub.ManifestV2(repository, tag)
	if err != nil {
		return -1, fmt.Errorf("Failed to fetch the manifest for %s/%s:%s. %v", srcHub.URL, repository, tag, err)
	}

	resp, err := manifest.MarshalJSON()

	srcHub.Logf(string(resp))

	//Migrate config object first
	err = migrateLayer(srcHub, destHub, repository, repository, manifest.Config)
	if err != nil {
		return -1, fmt.Errorf("Failed to migrate image layer. %v", err)
	}

	for _, layer := range manifest.Layers {
		srcHub.Logf("Uploading Layer: %s", layer.Digest)
		err := migrateLayer(srcHub, destHub, repository, repository, layer)
		if err != nil {
			return -1, fmt.Errorf("Failed to migrate image layer. %v", err)
		}
	}

	err = destHub.PutManifestV2(repository, tag, manifest)
	if err != nil {
		return -1, fmt.Errorf("Failed to upload manifest to %s/%s:%s. %v", destHub.URL, repository, tag, err)
	}

	/*

		err = destHub.PutManifest(repository, tag, manifest)
		if err != nil {
			return -1, fmt.Errorf("Failed to upload manifest to %s/%s:%s. %v", destHub.URL, repository, tag, err)
		}
	*/
	fmt.Printf("Copied Docker Image %s:%s successfully.\n", repository, tag)
	return 0, nil
}

/*
RepositoryArguments
*/
type RepositoryArguments struct {
	RegistryURL *string
	Repository  *string
	Tag         *string
}

func buildRegistryArguments(argPrefix string, argDescription string) RepositoryArguments {
	registryURLName := fmt.Sprintf("%sURL", argPrefix)
	registryURLDescription := fmt.Sprintf("URL of %s registry", argDescription)
	registryURLArg := kingpin.Flag(registryURLName, registryURLDescription).String()

	repositoryName := fmt.Sprintf("%sRepo", argPrefix)
	repositoryDescription := fmt.Sprintf("Name of the %s repository", argDescription)
	repositoryArg := kingpin.Flag(repositoryName, repositoryDescription).String()

	tagName := fmt.Sprintf("%sTag", argPrefix)
	tagDescription := fmt.Sprintf("Name of the %s tag", argDescription)
	tagArg := kingpin.Flag(tagName, tagDescription).String()

	return RepositoryArguments{
		RegistryURL: registryURLArg,
		Repository:  repositoryArg,
		Tag:         tagArg,
	}
}

func connectToRegistry(args RepositoryArguments, auths map[string]docker.AuthConfiguration) (*registry.Registry, error) {

	url := *args.RegistryURL

	urlWithoutPrefix := strings.TrimPrefix(url, "https://")
	urlWithoutPrefix = strings.TrimPrefix(urlWithoutPrefix, "http://")

	username := ""
	password := ""

	if auth, ok := auths[urlWithoutPrefix]; ok {
		username = auth.Username
		password = auth.Password
	}

	registry, err := registry.NewInsecure(url, username, password)
	if err != nil {
		return nil, fmt.Errorf("Failed to create registry connection for %s. %v", url, err)
	}

	err = registry.Ping()
	if err != nil {
		return nil, fmt.Errorf("Failed to to ping registry %s as a connection test. %v", url, err)
	}

	return registry, nil
}

func listRepositoriesAndTags(srcHub *registry.Registry, destHub *registry.Registry) (int, error) {

	exitCode := 0

	repositories, err := srcHub.Repositories()
	if err != nil {
		return -1, fmt.Errorf("Failed to list repositories for %s", srcHub.URL)
	}

	for _, repository := range repositories {
		tags, err := srcHub.Tags(repository)
		if err != nil {
			return -1, fmt.Errorf("Failed to list tags for %s", repository)
		}
		for _, tag := range tags {
			fmt.Printf("%s:%s\n", repository, tag)

			_, err = copyImage(srcHub, destHub, repository, tag)
			if err != nil {
				exitCode = -1
				fmt.Printf("An error occured: %s\n", err)
			}
		}
	}

	return exitCode, nil

}

func main() {
	exitCode := 0
	defer func() {
		os.Exit(exitCode)
	}()

	auths, err := docker.NewAuthConfigurationsFromDockerCfg()
	if err != nil {
		fmt.Printf("Couldn't read config.json in .docker folder\n")
	}

	srcArgs := buildRegistryArguments("src", "source")
	destArgs := buildRegistryArguments("dest", "destiation")
	repoArg := kingpin.Flag("repo", "The repository in the source and the destiation. Values provided by --srcRepo or --destTag will override this value").String()
	tagArg := kingpin.Flag("tag", "The tag name in the source and the destiation. Values provided by --srcTag or --destTag will override this value").Default("latest").String()
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

	/*

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

	*/

	srcHub, err := connectToRegistry(srcArgs, auths.Configs)
	if err != nil {
		fmt.Printf("Failed to establish a connection to the source registry. %v", err)
		exitCode = -1
		return
	}

	destHub, err := connectToRegistry(destArgs, auths.Configs)
	if err != nil {
		fmt.Printf("Failed to establish a connection to the destination registry. %v", err)
		exitCode = -1
		return
	}

	exitCode, err = listRepositoriesAndTags(srcHub, destHub)
	if err != nil {
		fmt.Println(err)
	}

}
