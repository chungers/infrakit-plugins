package fusion

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/infrakit/spi/instance"
	"github.com/hooklift/govix"
)

// CreateInstanceRequest is the concrete provision request type.
type CreateInstanceRequest struct {
	Tags          map[string]string
	MemorySizeMBs uint
	NumCPUs       uint
	LaunchGUI     bool
}

var DefaultCreateInstanceRequest = CreateInstanceRequest{
	MemorySizeMBs: 512,
	NumCPUs:       1,
	LaunchGUI:     false,
}

// Provisioner is what manages the VMs
type Provisioner struct {

	// Host is where the VMs run
	Host *vix.Host

	// VMDir is the directory where all VM files live -- vmx, vmdk, etc.
	VMDir string

	// VMXFilePath to the VMX file
	VMXFilePath string

	// SourceVM is the VM to clone from when creating new instances
	SourceVM *vix.VM

	// For opening VMX files, etc.
	password string

	// For removing the vm directories on destroy
	cleanupDirs chan string
}

// NewInstancePlugin creates a new plugin that creates instances using VMWare Fusion via VIX api
func NewInstancePlugin(vmDir, vmxPath, password string) (*Provisioner, error) {

	if err := os.MkdirAll(vmDir, 0644); err != nil {
		return nil, err
	}

	host, err := vix.Connect(vix.ConnectConfig{
		Provider: vix.VMWARE_WORKSTATION,
	})

	if err != nil {
		return nil, err
	}

	vm, err := host.OpenVM(vmxPath, password)
	if err != nil {
		return nil, err
	}

	cleanupDirs := make(chan string, 64) // buffered so Destroy won't block
	go func() {
		for path := range cleanupDirs {
			cleanupVMState(vmDir, path)
		}
	}()

	return &Provisioner{
		Host:        host,
		VMDir:       vmDir,
		VMXFilePath: vmxPath,
		SourceVM:    vm,
		password:    password,
		cleanupDirs: cleanupDirs,
	}, nil
}

// Just moves the vm to a trash folder...  Feel free to change this..
func cleanupVMState(vmDir, path string) {

	trashDir := filepath.Join(vmDir, ".trash")

	os.MkdirAll(trashDir, 0755)

	vmxDir := filepath.Dir(path)
	newpath := filepath.Join(trashDir, filepath.Base(vmxDir))

	err := os.Rename(vmxDir, newpath)

	log.Debugln("Renaming", vmxDir, "to", newpath, "err=", err)
}

// Shutdown does cleanup
func (p *Provisioner) Shutdown() {
	close(p.cleanupDirs)

	if p.Host != nil {
		log.Infoln("Disconnecting from VM host")
		p.Host.Disconnect()
	}
}

// Validate performs local checks to determine if the request is valid.
func (p *Provisioner) Validate(req json.RawMessage) error {
	model := DefaultCreateInstanceRequest
	if err := json.Unmarshal(req, &model); err != nil {
		return err
	}
	return nil
}

// Provision creates a new instance.
func (p *Provisioner) Provision(spec instance.Spec) (*instance.ID, error) {
	if spec.Properties == nil {
		return nil, errors.New("Properties must be set")
	}

	request := CreateInstanceRequest{}
	err := json.Unmarshal(*spec.Properties, &request)
	if err != nil {
		return nil, fmt.Errorf("Invalid input formatting: %s", err)
	}

	name := fmt.Sprintf("instance-%d", time.Now().Unix())
	path := filepath.Join(p.VMDir, name)

	os.MkdirAll(path, 0755) // needs to be executable

	path = filepath.Join(path, name+".vmx")

	instanceID := instance.ID(name)

	log.Debugln("Cloning", instanceID, "into", path)

	// Do a full clone so that there are no links --- more storage required
	clone, err := p.SourceVM.Clone(vix.CLONETYPE_FULL, path)
	if err != nil {
		return nil, err
	}
	return &instanceID, vmStart(path, clone, instanceID, spec)
}

// Destroy terminates an existing instance.
func (p *Provisioner) Destroy(id instance.ID) error {

	matches, err := p.findRunning(func(vm *vix.VM) bool {
		d, err := vm.DisplayName()
		if err == nil {
			return d == string(id)
		}
		return false
	})

	if err != nil {
		return err
	}

	if len(matches) == 0 {
		return nil
	}

	for _, vm := range matches {
		err := vmStop(vm, p.cleanupDirs)
		if err != nil {
			log.Warningln("Destroy vm failed", id, "err=", err)
		} else {
			log.Debugln("Destroyed vm", id)
		}
	}
	return nil
}

// DescribeInstances implements instance.Provisioner.DescribeInstances.
func (p *Provisioner) DescribeInstances(tags map[string]string) ([]instance.Description, error) {

	result := []instance.Description{}

	_, err := p.findRunning(func(vm *vix.VM) bool {

		displayName, err := vm.DisplayName()
		if err != nil {
			log.Warningln("Err getting display name from", vm, err)
			return false
		}

		vmxpath, err := vm.VmxPath()
		if err != nil {
			return false
		}

		specPath := filepath.Join(filepath.Dir(vmxpath), "infrakit.spec")

		log.Debugln("Checking", displayName, "path=", vmxpath, "spec=", specPath)

		buff, err := ioutil.ReadFile(specPath)
		if err != nil {
			log.Warningln("Err reading spec file", err, "vmxpath=", vmxpath)
			return false
		}

		spec := instance.Spec{}
		err = json.Unmarshal(buff, &spec)
		if err != nil {
			log.Warningln("Err unmarshaling spec file", err, "vmxpath=", vmxpath)
			return false
		}

		match := false
		if len(tags) == 0 {
			match = true
		} else {
			for k, v := range spec.Tags {
				if tags[k] == v {
					match = true
					break
				}
			}
		}

		if match {
			result = append(result, instance.Description{
				ID:        instance.ID(displayName),
				LogicalID: spec.LogicalID,
				Tags:      spec.Tags,
			})
		}
		return match
	})

	return result, err
}
