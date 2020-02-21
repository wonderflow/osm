package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/deislabs/smc/pkg/certificate"
	"github.com/deislabs/smc/pkg/tresor"
)

const installDesc = `
This command installs the smc control plane on the Kubernetes cluster.
`

type installCmd struct {
	out                     io.Writer
	namespace               string
	containerRegistry       string
	containerRegistrySecret string

	kubeClient  kubernetes.Interface
	rootcertpem []byte
	rootkeypem  []byte
	certManager *tresor.CertManager
}

func newInstallCmd(out io.Writer) *cobra.Command {

	install := &installCmd{
		out: out,
	}

	cmd := &cobra.Command{
		Use:   "install",
		Short: "install smc control plane",
		Long:  installDesc,
		RunE: func(_ *cobra.Command, args []string) error {
			return install.run()
		},
	}

	f := cmd.Flags()
	f.StringVarP(&install.namespace, "namespace", "n", "smc", "namespace to install control plane components")
	f.StringVar(&install.containerRegistry, "container-registry", "smctest.azurecr.io", "container registry that hosts control plane component images")
	f.StringVar(&install.containerRegistrySecret, "container-registry-secret", "acr-creds", "name of the container registry Kubernetes Secret that contains container registry credentials")

	return cmd
}

func (i *installCmd) run() error {
	context := "" //TOOD: make context flag
	client, err := getKubeClient(context)
	if err != nil {
		return err
	}
	//TODO create namespace if it doesn't already exist
	i.kubeClient = client

	if err = i.bootstrapRootCert(); err != nil {
		return err
	}

	if err := i.deploy("cds", 15125); err != nil {
		return err
	}
	if err := i.deploy("sds", 15123); err != nil {
		return err
	}
	if err := i.deploy("eds", 15124); err != nil {
		return err
	}
	if err := i.deploy("rds", 15126); err != nil {
		return err
	}

	return nil
}

func (i *installCmd) bootstrapRootCert() error {
	// generate root cert and key
	org := "Azure Mesh"
	minsValid := time.Duration(20) * time.Minute
	certpem, keypem, cert, key, err := tresor.NewCA(org, minsValid)
	if err != nil {
		return err
	}
	i.rootcertpem = certpem
	i.rootkeypem = keypem

	i.certManager, err = tresor.NewCertManagerWithCA(cert, key, org, minsValid)
	return err
}

func (i *installCmd) generateCerts(name string) error {
	host := fmt.Sprintf("%s.azure.mesh", name)
	cert, err := i.certManager.IssueCertificate(certificate.CommonName(host))
	if err != nil {
		return err
	}

	configmap := generateCertConfig(fmt.Sprintf("ca-rootcertpemstore-%s", name), i.namespace, "root-cert.pem", i.rootcertpem)
	if _, err := i.kubeClient.CoreV1().ConfigMaps(i.namespace).Create(configmap); err != nil {
		return err
	}

	configmap = generateCertConfig(fmt.Sprintf("ca-certpemstore-%s", name), i.namespace, "cert.pem", cert.GetCertificateChain())
	if _, err := i.kubeClient.CoreV1().ConfigMaps(i.namespace).Create(configmap); err != nil {
		return err
	}

	configmap = generateCertConfig(fmt.Sprintf("ca-keypemstore-%s", name), i.namespace, "key.pem", cert.GetPrivateKey())
	if _, err := i.kubeClient.CoreV1().ConfigMaps(i.namespace).Create(configmap); err != nil {
		return err
	}

	return nil
}

func (i *installCmd) deploy(name string, port int32) error {
	if err := i.generateCerts(name); err != nil {
		return err
	}

	deployment, service := generateKubernetesConfig(name, i.namespace, i.containerRegistry, i.containerRegistrySecret, port)

	if _, err := i.kubeClient.AppsV1().Deployments(i.namespace).Create(deployment); err != nil {
		return err
	}

	if _, err := i.kubeClient.CoreV1().Services(i.namespace).Create(service); err != nil {
		return err
	}

	return nil
}

func getKubeClient(context string) (kubernetes.Interface, error) {
	kubeConfigPath := filepath.Join(homeDir(), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

func int32Ptr(i int32) *int32 { return &i }