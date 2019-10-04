package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/ghodss/yaml"
	"github.com/google/gopacket/pcap"
	"github.com/gourmetproject/gourmet"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"plugin"
)

var (
	flagConfig = flag.String("c", "", "Gourmet configuration file")
)

func main() {
	var c *gourmet.Config
	var err error
	flag.Parse()
	if *flagConfig != "" {
		c, err = parseConfigFile(*flagConfig)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		c, err = parseConfigFile("config.yml")
		if err != nil {
			log.Fatal(err)
		}
	}
	setDefaults(c)
	err = validateConfig(c)
	if err != nil {
		log.Fatal(err)
	}
	ifaceType, err := convertIfaceType(c.InterfaceType)
	if err != nil {
		log.Fatal(err)
	}
	analyzers, err := newAnalyzers(c.Analyzers, c.UpdateAnalyzers)
	if err != nil {
		log.Fatal(err)
	}
	opts := &gourmet.SensorOptions{
		InterfaceName: c.Interface,
		InterfaceType: ifaceType,
		IsPromiscuous: c.Promiscuous,
		SnapLen:       uint32(c.SnapLen),
		Bpf:           c.Bpf,
		LogFileName:   c.LogFile,
		Analyzers:     analyzers,
	}
	gourmet.Start(opts)
}

func parseConfigFile(cf string) (c *gourmet.Config, err error) {
	c = &gourmet.Config{}
	contents, err := ioutil.ReadFile(cf)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(contents, c)
	if err != nil {
		fmt.Printf("%#v\n", c)
		return nil, err
	}
	return c, err
}

func setDefaults(c *gourmet.Config) {
	if c.SnapLen == 0 {
		c.SnapLen = 262144
	}
	if c.LogFile == "" {
		c.LogFile = "gourmet.log"
	}
	if c.InterfaceType == "" {
		c.InterfaceType = "libpcap"
	}
}

func validateConfig(c *gourmet.Config) (err error) {
	if err = validateInterface(c.Interface); err != nil {
		return err
	}
	if err = validateSnapshotLength(c.SnapLen); err != nil {
		return err
	}
	return nil
}

func validateInterface(iface string) error {
	devices, err := pcap.FindAllDevs()
	if err != nil {
		log.Fatal(err)
	}
	for _, device := range devices {
		if device.Name == iface {
			return nil
		}
	}
	return errors.New("specified network interface does not exist")
}

func validateSnapshotLength(snapLen int) error {
	if snapLen < 64 {
		return errors.New("minimum snapshot length is 64")
	}
	if snapLen > 4294967295 {
		return errors.New("snapshot length must be an unsigned 32-bit integer")
	}
	return nil
}

func convertIfaceType(ifaceType string) (gourmet.InterfaceType, error) {
	if ifaceType == "libpcap" {
		return gourmet.LibpcapType, nil
	} else if ifaceType == "afpacket" {
		return gourmet.AfpacketType, nil
	} else {
		return 0, errors.New("invalid interface type. Must be libpcap or afpacket")
	}
}

func newAnalyzers(links map[string]interface{}, update bool) (analyzers []gourmet.Analyzer, err error) {
	usr, err := user.Current()
	if err != nil {
		return nil, err
	}
	homeDir := usr.HomeDir
	pluginsDir := filepath.Join(homeDir, ".gourmet/plugins/")
	var analyzerFiles []string
	for link := range links {
		pluginDir := filepath.Join(pluginsDir, link)
		mainPath := filepath.Join(pluginDir, "main.go")
		exists, err := dirExists(pluginDir); if err != nil {
			return nil, err
		}
		if !exists {
			fmt.Printf("[*] Installing %s\n", link)
			err = exec.Command("git", "clone", fmt.Sprintf("https://%s", link), pluginDir).Run()
			if err != nil {
				return nil, errors.New(fmt.Sprintf("failed to install %s: %s", link, err.Error()))
			}
		} else if update {
			fmt.Printf("[*] Updating %s\n", link)
			err = exec.Command("git", "-C", pluginDir, "pull").Run()
		}
		_, err = os.Stat(mainPath)
		if err != nil {
			return nil, err
		}
		analyzerFiles = append(analyzerFiles, mainPath)
	}
	if len(analyzerFiles) > 0 {
		for _, analyzerFile := range analyzerFiles {
			folderName := filepath.Dir(analyzerFile)
			fmt.Printf("[*] Building %s\n", filepath.Base(filepath.Dir(analyzerFile)))
			out, err := exec.Command("go", "build", "-buildmode=plugin", "-o",
				fmt.Sprintf("%s/main.so", filepath.Dir(analyzerFile)), analyzerFile).CombinedOutput()
			if err != nil {
				return nil, errors.New(
					fmt.Sprintf("failed to build %s: %s", analyzerFile, string(out)))
			}
			p, err := plugin.Open(fmt.Sprintf("%s/main.so", folderName))
			if err != nil {
				return nil, err
			}
			newAnalyzerFunc, err := p.Lookup("NewAnalyzer")
			if err != nil {
				return nil, err
			}
			analyzer := newAnalyzerFunc.(func() gourmet.Analyzer)()
			analyzers = append(analyzers, analyzer)
		}
	}
	return analyzers, nil
}

func dirExists(path string) (bool, error)  {
	_, err := os.Stat(path); if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}
