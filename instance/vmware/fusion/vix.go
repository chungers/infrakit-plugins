package fusion

import (
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/infrakit/spi/instance"
	"github.com/hooklift/govix"
)

func (p *Provisioner) findRunning(matcher func(*vix.VM) bool) ([]*vix.VM, error) {
	if p.Host == nil {
		return nil, nil
	}
	urls, err := p.Host.FindItems(vix.FIND_RUNNING_VMS)
	if err != nil {
		return nil, err
	}

	matches := []*vix.VM{}
	for _, url := range urls {
		vm, err := p.Host.OpenVM(url, p.password)
		if err != nil {
			continue
		}
		if matcher(vm) {
			matches = append(matches, vm)
		}
	}
	return matches, nil
}

func vmStart(path string, vm *vix.VM, id instance.ID, spec instance.Spec) error {
	buff, err := json.Marshal(spec)
	if err != nil {
		return err
	}

	// Write the whole spec to a file instead of setting annotation because that
	// can corrupt the entire vmx file if special characters are included.

	p := filepath.Join(filepath.Dir(path), "infrakit.spec")
	err = ioutil.WriteFile(p, buff, 0644)
	if err != nil {
		return err
	}
	log.Debugln("Written spec to", p)

	vm.SetDisplayName(string(id))

	config := DefaultCreateInstanceRequest

	if spec.Properties != nil {
		err = json.Unmarshal([]byte(*spec.Properties), &config)
		if err != nil {
			return err
		}
	}

	vm.SetMemorySize(config.MemorySizeMBs)
	vm.SetNumberVcpus(config.NumCPUs)

	// TODO - need to add support for copying in the spec.Init

	log.Infoln("Powering up", id)

	mode := vix.VMPOWEROP_NORMAL
	if config.LaunchGUI {
		mode = vix.VMPOWEROP_LAUNCH_GUI
	}
	err = vm.PowerOn(mode)
	if err != nil {
		return err
	}

	tick := time.Tick(1 * time.Second)
	for range tick {
		running, err := vm.IsRunning()
		log.Infoln("Checking vm running=", running, "err=", err)
		if err != nil {
			return err
		}
		if running {
			break
		}
	}

	for range tick {
		state, err := vm.PowerState()
		log.Infoln("VM power state=", state, "err=", err)
		if err != nil {
			return err
		}
		if state&vix.POWERSTATE_POWERED_ON > 0 {
			break
		}
	}

	return nil
}

func vmStop(vm *vix.VM, cleanup chan<- string) error {

	powerOff := vix.VMPOWEROP_NORMAL
	// check to see if VMWare Tools are running in the guest OS
	if state, err := vm.ToolsState(); err == nil && state == vix.TOOLSSTATE_RUNNING {
		powerOff = vix.VMPOWEROP_FROM_GUEST
	}
	err := vm.PowerOff(powerOff)
	if err != nil {
		return err
	}

	vmxPath, err := vm.VmxPath()
	if err != nil {
		return err
	}
	cleanup <- vmxPath

	tick := time.Tick(1 * time.Second)
	for range tick {
		running, err := vm.IsRunning()
		log.Infoln("Checking vm running=", running, "err=", err)
		if err != nil {
			return err
		}
		if !running {
			break
		}
	}

	for range tick {
		state, err := vm.PowerState()
		log.Infoln("VM power state=", state, "err=", err)
		if err != nil {
			return err
		}
		if state&vix.POWERSTATE_POWERED_OFF > 0 {
			break
		}
	}
	return nil
}
