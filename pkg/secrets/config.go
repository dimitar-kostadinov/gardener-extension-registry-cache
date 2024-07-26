package secrets

import (
	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/registry/v1alpha3"
	extensionssecretsmanager "github.com/gardener/gardener/extensions/pkg/util/secret/manager"
	secretutils "github.com/gardener/gardener/pkg/utils/secrets"
	secretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager"
	"k8s.io/utils/ptr"
	"net"
	"time"
)

const (
	// ManagerIdentity is the identity used for the Secrets Manager.
	ManagerIdentity = "extension-registry-cache"
	// CAName is the name of the CA secret.
	CAName = "ca-extension-registry-cache"
)

// ConfigsFor returns configurations for the secrets manager for the given registry caches statuses.
func ConfigsFor(caches []v1alpha3.RegistryCacheStatus) []extensionssecretsmanager.SecretConfigWithOptions {
	configs := []extensionssecretsmanager.SecretConfigWithOptions{
		{
			Config: &secretutils.CertificateSecretConfig{
				Name:       CAName,
				CommonName: CAName,
				CertType:   secretutils.CACert,
				Validity:   ptr.To(730 * 24 * time.Hour),
			},
			Options: []secretsmanager.GenerateOption{secretsmanager.Persist()},
		},
	}
	for _, cache := range caches {
		configs = append(configs, extensionssecretsmanager.SecretConfigWithOptions{
			Config: &secretutils.CertificateSecretConfig{
				Name:                        cache.Upstream + "-tls",
				CommonName:                  cache.Upstream + "-tls",
				CertType:                    secretutils.ServerCert,
				IPAddresses:                 []net.IP{net.ParseIP(cache.ClusterIP).To4()},
				Validity:                    ptr.To(90 * 24 * time.Hour),
				SkipPublishingCACertificate: true,
			},
			Options: []secretsmanager.GenerateOption{secretsmanager.SignedByCA(CAName, secretsmanager.UseOldCA)},
		})
	}
	return configs
}
