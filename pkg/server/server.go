package server

import (
	"encoding/json"
	"fmt"
	"net/url"

	igntypes "github.com/coreos/ignition/config/v2_2/types"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	ctrlcommon "github.com/openshift/machine-config-operator/pkg/controller/common"
	daemonconsts "github.com/openshift/machine-config-operator/pkg/daemon/constants"
	"github.com/vincent-petithory/dataurl"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// defaultMachineKubeConfPath defines the default location
	// of the KubeConfig file on the machine.
	defaultMachineKubeConfPath = "/etc/kubernetes/kubeconfig"

	// defaultFileSystem defines the default file system to be
	// used for writing the ignition files created by the
	// server.
	defaultFileSystem = "root"
)

// kubeconfigFunc fetches the kubeconfig that needs to be served.
type kubeconfigFunc func() (kubeconfigData []byte, rootCAData []byte, err error)

// appenderFunc appends Config.
type appenderFunc func(*runtime.RawExtension) error

// Server defines the interface that is implemented by different
// machine config server implementations.
type Server interface {
	GetConfig(poolRequest) (*runtime.RawExtension, error)
}

func getAppenders(currMachineConfig string, f kubeconfigFunc, osimageurl string) []appenderFunc {
	appenders := []appenderFunc{
		// append machine annotations file.
		func(config *runtime.RawExtension) error { return appendNodeAnnotations(config, currMachineConfig) },
		// append pivot
		func(config *runtime.RawExtension) error { return appendInitialPivot(config, osimageurl) },
		// append kubeconfig.
		func(config *runtime.RawExtension) error { return appendKubeConfig(config, f) },
	}
	return appenders
}

// machineConfigToIgnition converts a MachineConfig object into raw Ignition.
func machineConfigToIgnition(mccfg *mcfgv1.MachineConfig) *runtime.RawExtension {
	tmpcfg := mccfg.DeepCopy()
	newcfg := ctrlcommon.NewIgnConfig()
	newRawIgn, err := mcfgv1.EncodeIgnitionConfigSpecV2(&newcfg)
	if err != nil {
		panic(err.Error())
	}
	tmpcfg.Spec.Config.Raw = newRawIgn

	serialized, err := json.Marshal(tmpcfg)
	if err != nil {
		panic(err.Error())
	}
	appendFileToIgnition(&mccfg.Spec.Config, daemonconsts.MachineConfigEncapsulatedPath, string(serialized))

	return &mccfg.Spec.Config
}

// Golang :cry:
func boolToPtr(b bool) *bool {
	return &b
}

func appendInitialPivot(raw *runtime.RawExtension, osimageurl string) error {
	if osimageurl == "" {
		return nil
	}

	// Tell pivot.service to pivot early
	appendFileToIgnition(raw, daemonconsts.EtcPivotFile, osimageurl+"\n")
	conf, err := mcfgv1.DecodeIgnitionConfigSpecV2(raw.Raw)
	if err != nil {
		return err
	}
	// Awful hack to create a file in /run
	// https://github.com/openshift/machine-config-operator/pull/363#issuecomment-463397373
	// "So one gotcha here is that Ignition will actually write `/run/pivot/image-pullspec` to the filesystem rather than the `/run` tmpfs"
	if len(conf.Systemd.Units) == 0 {
		conf.Systemd.Units = make([]igntypes.Unit, 0)
	}
	unit := igntypes.Unit{
		Name:    "mcd-write-pivot-reboot.service",
		Enabled: boolToPtr(true),
		Contents: `[Unit]
Before=pivot.service
ConditionFirstBoot=true
[Service]
ExecStart=/bin/sh -c 'mkdir /run/pivot && touch /run/pivot/reboot-needed'
[Install]
WantedBy=multi-user.target
`}
	conf.Systemd.Units = append(conf.Systemd.Units, unit)
	raw.Raw, err = mcfgv1.EncodeIgnitionConfigSpecV2(conf)
	if err != nil {
		return err
	}
	return nil
}

func appendKubeConfig(raw *runtime.RawExtension, f kubeconfigFunc) error {
	kcData, _, err := f()
	if err != nil {
		return err
	}
	appendFileToIgnition(raw, defaultMachineKubeConfPath, string(kcData))
	return nil
}

func appendNodeAnnotations(raw *runtime.RawExtension, currConf string) error {

	anno, err := getNodeAnnotation(currConf)
	if err != nil {
		return err
	}
	appendFileToIgnition(raw, daemonconsts.InitialNodeAnnotationsFilePath, anno)
	return nil
}

func getNodeAnnotation(conf string) (string, error) {
	nodeAnnotations := map[string]string{
		daemonconsts.CurrentMachineConfigAnnotationKey:     conf,
		daemonconsts.DesiredMachineConfigAnnotationKey:     conf,
		daemonconsts.MachineConfigDaemonStateAnnotationKey: daemonconsts.MachineConfigDaemonStateDone,
	}
	contents, err := json.Marshal(nodeAnnotations)
	if err != nil {
		return "", fmt.Errorf("could not marshal node annotations, err: %v", err)
	}
	return string(contents), nil
}

func appendFileToIgnition(raw *runtime.RawExtension, outPath, contents string) {
	conf, err := mcfgv1.DecodeIgnitionConfigSpecV2(raw.Raw)
	if err != nil {
		panic(err.Error())
	}
	fileMode := int(420)
	file := igntypes.File{
		Node: igntypes.Node{
			Filesystem: defaultFileSystem,
			Path:       outPath,
		},
		FileEmbedded1: igntypes.FileEmbedded1{
			Contents: igntypes.FileContents{
				Source: getEncodedContent(contents),
			},
			Mode: &fileMode,
		},
	}
	if len(conf.Storage.Files) == 0 {
		conf.Storage.Files = make([]igntypes.File, 0)
	}
	conf.Storage.Files = append(conf.Storage.Files, file)
	raw.Raw, err = mcfgv1.EncodeIgnitionConfigSpecV2(conf)
	if err != nil {
		panic(err.Error())
	}
}

func getDecodedContent(inp string) (string, error) {
	d, err := dataurl.DecodeString(inp)
	if err != nil {
		return "", err
	}

	return string(d.Data), nil
}

func getEncodedContent(inp string) string {
	return (&url.URL{
		Scheme: "data",
		Opaque: "," + dataurl.Escape([]byte(inp)),
	}).String()
}
