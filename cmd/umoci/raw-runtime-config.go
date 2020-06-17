/*
 * umoci: Umoci Modifies Open Containers' Images
 * Copyright (C) 2016-2020 SUSE LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"fmt"
	"os"

	"github.com/apex/log"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/umoci"
	"github.com/opencontainers/umoci/oci/cas/dir"
	"github.com/opencontainers/umoci/oci/casext"
	"github.com/opencontainers/umoci/oci/layer"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"golang.org/x/net/context"
)

var rawConfigCommand = uxRemap(cli.Command{
	Name:    "runtime-config",
	Aliases: []string{"config"},
	Usage:   "generates an OCI runtime configuration for an image",
	ArgsUsage: `--image <image-path>[:<tag>] [--rootfs <rootfs>] <config.json>

Where "<image-path>" is the path to the OCI image, "<tag>" is the name of the
tagged image to unpack (if not specified, defaults to "latest"), "<rootfs>" is
a rootfs to use as a supplementary "source of truth" for certain generation
operations and "<config.json>" is the destination to write the runtime
configuration to.

Note that the results of this may not agree with umoci-unpack(1) because the
--rootfs flag affects how certain properties are interpreted.`,

	// unpack reads manifest information.
	Category: "image",

	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "rootfs",
			Usage: "path to secondary source of truth (root filesystem)",
		},
	},

	Action: rawConfig,

	Before: func(ctx *cli.Context) error {
		if ctx.NArg() != 1 {
			return errors.Errorf("invalid number of positional arguments: expected <config.json>")
		}
		if ctx.Args().First() == "" {
			return errors.Errorf("config.json path cannot be empty")
		}
		ctx.App.Metadata["config"] = ctx.Args().First()
		return nil
	},
})

func rawConfig(ctx *cli.Context) error {
	imagePath := ctx.App.Metadata["--image-path"].(string)
	fromName := ctx.App.Metadata["--image-tag"].(string)
	configPath := ctx.App.Metadata["config"].(string)

	var meta umoci.Meta
	meta.Version = umoci.MetaVersion

	// Parse and set up the mapping options.
	err := umoci.ParseIdmapOptions(&meta, ctx)
	if err != nil {
		return err
	}

	// Get a reference to the CAS.
	engine, err := dir.Open(imagePath)
	if err != nil {
		return errors.Wrap(err, "open CAS")
	}
	engineExt := casext.NewEngine(engine)
	defer engine.Close()

	fromDescriptorPaths, err := engineExt.ResolveReference(context.Background(), fromName)
	if err != nil {
		return errors.Wrap(err, "get descriptor")
	}
	if len(fromDescriptorPaths) == 0 {
		return errors.Errorf("tag not found: %s", fromName)
	}
	if len(fromDescriptorPaths) != 1 {
		// TODO: Handle this more nicely.
		return errors.Errorf("tag is ambiguous: %s", fromName)
	}
	meta.From = fromDescriptorPaths[0]

	manifestBlob, err := engineExt.FromDescriptor(context.Background(), meta.From.Descriptor())
	if err != nil {
		return errors.Wrap(err, "get manifest")
	}
	defer manifestBlob.Close()

	if manifestBlob.Descriptor.MediaType != ispec.MediaTypeImageManifest {
		return errors.Wrap(fmt.Errorf("descriptor does not point to ispec.MediaTypeImageManifest: not implemented: %s", manifestBlob.Descriptor.MediaType), "invalid --image tag")
	}

	// Get the manifest.
	manifest, ok := manifestBlob.Data.(ispec.Manifest)
	if !ok {
		// Should _never_ be reached.
		return errors.Errorf("[internal error] unknown manifest blob type: %s", manifestBlob.Descriptor.MediaType)
	}

	// Generate the configuration.
	configFile, err := os.Create(configPath)
	if err != nil {
		return errors.Wrap(err, "opening config path")
	}
	defer configFile.Close()

	// Write out the generated config.
	log.Info("generating config.json")
	if err := layer.UnpackRuntimeJSON(context.Background(), engineExt, configFile, ctx.String("rootfs"), manifest, &meta.MapOptions); err != nil {
		return errors.Wrap(err, "generate config")
	}
	return nil
}
