package vagrant

import (
	"fmt"
	"github.com/mitchellh/packer/common"
	"github.com/mitchellh/packer/packer"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type AWSBoxConfig struct {
	common.PackerConfig `mapstructure:",squash"`

	OutputPath          string `mapstructure:"output"`
	VagrantfileTemplate string `mapstructure:"vagrantfile_template"`

	tpl *packer.ConfigTemplate
}

type AWSVagrantfileTemplate struct {
	Images map[string]string
}

type AWSBoxPostProcessor struct {
	config AWSBoxConfig
}

func (p *AWSBoxPostProcessor) Configure(raws ...interface{}) error {
	md, err := common.DecodeConfig(&p.config, raws...)
	if err != nil {
		return err
	}

	p.config.tpl, err = packer.NewConfigTemplate()
	if err != nil {
		return err
	}
	p.config.tpl.UserVars = p.config.PackerUserVars

	// Accumulate any errors
	errs := common.CheckUnusedConfig(md)

	validates := map[string]*string{
		"output":               &p.config.OutputPath,
		"vagrantfile_template": &p.config.VagrantfileTemplate,
	}

	for n, ptr := range validates {
		if err := p.config.tpl.Validate(*ptr); err != nil {
			errs = packer.MultiErrorAppend(
				errs, fmt.Errorf("Error parsing %s: %s", n, err))
		}
	}

	if errs != nil && len(errs.Errors) > 0 {
		return errs
	}

	return nil
}

func (p *AWSBoxPostProcessor) PostProcess(ui packer.Ui, artifact packer.Artifact) (packer.Artifact, bool, error) {
	// Determine the regions...
	tplData := &AWSVagrantfileTemplate{
		Images: make(map[string]string),
	}

	for _, regions := range strings.Split(artifact.Id(), ",") {
		parts := strings.Split(regions, ":")
		if len(parts) != 2 {
			return nil, false, fmt.Errorf("Poorly formatted artifact ID: %s", artifact.Id())
		}

		tplData.Images[parts[0]] = parts[1]
	}

	// Compile the output path
	outputPath, err := p.config.tpl.Process(p.config.OutputPath, &OutputPathTemplate{
		ArtifactId: artifact.Id(),
		BuildName:  p.config.PackerBuildName,
		Provider:   "aws",
	})
	if err != nil {
		return nil, false, err
	}

	// Create a temporary directory for us to build the contents of the box in
	dir, err := ioutil.TempDir("", "packer")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(dir)

	// Create the Vagrantfile from the template
	vf, err := os.Create(filepath.Join(dir, "Vagrantfile"))
	if err != nil {
		return nil, false, err
	}
	defer vf.Close()

	vagrantfileContents := defaultAWSVagrantfile
	if p.config.VagrantfileTemplate != "" {
		log.Printf("Using vagrantfile template: %s", p.config.VagrantfileTemplate)
		f, err := os.Open(p.config.VagrantfileTemplate)
		if err != nil {
			err = fmt.Errorf("error opening vagrantfile template: %s", err)
			return nil, false, err
		}
		defer f.Close()

		contents, err := ioutil.ReadAll(f)
		if err != nil {
			err = fmt.Errorf("error reading vagrantfile template: %s", err)
			return nil, false, err
		}

		vagrantfileContents = string(contents)
	}

	vagrantfileContents, err = p.config.tpl.Process(vagrantfileContents, tplData)
	if err != nil {
		return nil, false, fmt.Errorf("Error writing Vagrantfile: %s", err)
	}
	vf.Write([]byte(vagrantfileContents))
	vf.Close()

	// Create the metadata
	metadata := map[string]string{"provider": "aws"}
	if err := WriteMetadata(dir, metadata); err != nil {
		return nil, false, err
	}

	// Compress the directory to the given output path
	if err := DirToBox(outputPath, dir, ui); err != nil {
		err = fmt.Errorf("error creating box: %s", err)
		return nil, false, err
	}

	return NewArtifact("aws", outputPath), true, nil
}

var defaultAWSVagrantfile = `
Vagrant.configure("2") do |config|
  config.vm.provider "aws" do |aws|
    {{ range $region, $ami := .Images }}
	aws.region_config "{{ $region }}", ami: "{{ $ami }}"
	{{ end }}
  end
end
`
