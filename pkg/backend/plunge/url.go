// Copyright 2026, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package plunge

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"k8s.io/client-go/kubernetes"

	"github.com/intentius/choudoufu/pkg/kubesweep"
	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/pulumi/pulumi/pkg/v3/backend/diy"
)

// URLPrefix marks a plunge backend URL: "plunge+" ahead of a DIY backend URL. The DIY URL after it keeps the
// stack metadata (which stacks exist, their tags and history) exactly where the DIY backend keeps it; the records
// live beside it under ".pulumi/records".
//
//	plunge+file:///path/to/dir        local records, one host only
//	plunge+s3://bucket/prefix         S3 conditional writes (region, profile, endpoint, s3ForcePathStyle query keys)
//	plunge+azblob://container/prefix  Azure Blob ETag conditions (storage_account query key or AZURE_STORAGE_ACCOUNT;
//	                                  resource_group and subscription_id for the account-settings contract;
//	                                  endpoint and metadata_host for an emulator, azblobstore.go)
//	plunge+gs://bucket/prefix         GCS generation preconditions
//	plunge+k8s://namespace/prefix     one Kubernetes Secret per record, resourceVersion preconditions (metadata,
//	                                  kubeconfig, context, in_cluster and estate query keys)
//
// plunge+k8s:// is the one URL with no DIY URL inside it, since the DIY backend has no Kubernetes store. Its
// stack metadata lives at the DIY URL its metadata query key names (plunge+k8s://records?metadata=s3://bucket).
const URLPrefix = "plunge+"

// k8sScheme is the scheme of a plunge+k8s:// URL.
const k8sScheme = "k8s"

// EnvAllowInsecure names record store settings whose failed check a run proceeds past, comma separated, as the
// allow_insecure query key does. The setting names are staterecord's: blob_versioning, lifecycle_management and
// public_access for Azure Blob; versioning, lifecycle and public_access_block for S3; object_versioning,
// noncurrent_lifecycle, public_access_prevention and uniform_bucket_level_access for GCS.
const EnvAllowInsecure = "PLUNGE_ALLOW_INSECURE"

// recordsDir is where the records live relative to the DIY backend root.
const recordsDir = ".pulumi/records"

// IsPlungeBackendURL reports whether u is a plunge backend URL with a DIY URL scheme this build supports.
func IsPlungeBackendURL(u string) bool {
	inner, ok := strings.CutPrefix(u, URLPrefix)
	if !ok {
		return false
	}
	scheme, _, _ := strings.Cut(inner, "://")
	switch scheme {
	case "file", "s3", "azblob", "gs":
		return diy.IsDIYBackendURL(inner)
	case k8sScheme:
		return true
	}
	return false
}

// innerURL is the DIY URL inside a plunge URL.
func innerURL(u string) string { return strings.TrimPrefix(u, URLPrefix) }

// isK8sURL reports whether a plunge URL names the Kubernetes record store.
func isK8sURL(u string) bool { return strings.HasPrefix(innerURL(u), k8sScheme+"://") }

// MissingMetadataURLError refuses a plunge+k8s:// URL that names no DIY URL for the stack metadata.
type MissingMetadataURLError struct {
	URL string
}

func (e *MissingMetadataURLError) Error() string {
	return fmt.Sprintf("plunge: %q keeps its records in Kubernetes Secrets, and the stack metadata (which stacks "+
		"exist, their tags, the default secrets manager) needs a DIY backend URL of its own: add a metadata query "+
		"key, for example %s%s://records?metadata=file:///shared/plunge-meta or metadata=s3://bucket/prefix",
		e.URL, URLPrefix, k8sScheme)
}

// metadataURL is the DIY backend URL that keeps a plunge URL's stack metadata: the URL inside it, or for
// plunge+k8s:// the one its metadata query key names.
func metadataURL(plungeURL string) (string, error) {
	if !isK8sURL(plungeURL) {
		return innerURL(plungeURL), nil
	}
	u, err := url.Parse(innerURL(plungeURL))
	if err != nil {
		return "", fmt.Errorf("plunge: parsing %q: %w", plungeURL, err)
	}
	meta := u.Query().Get("metadata")
	if meta == "" {
		return "", &MissingMetadataURLError{URL: plungeURL}
	}
	if !diy.IsDIYBackendURL(meta) {
		return "", fmt.Errorf("plunge: the metadata query key of %q is %q, which is not a DIY backend URL "+
			"(file://, s3://, azblob://, gs://)", plungeURL, meta)
	}
	return meta, nil
}

// allowInsecure is the record store settings a run is told to proceed past: the URL's allow_insecure query key and
// PLUNGE_ALLOW_INSECURE, both comma separated.
func allowInsecure(q url.Values) []string {
	var out []string
	for _, list := range []string{q.Get("allow_insecure"), os.Getenv(EnvAllowInsecure)} {
		for _, name := range strings.Split(list, ",") {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// openStore opens the record store a plunge URL names.
func openStore(ctx context.Context, plungeURL string) (staterecord.Store, error) {
	inner := innerURL(plungeURL)
	u, err := url.Parse(inner)
	if err != nil {
		return nil, fmt.Errorf("plunge: parsing %q: %w", plungeURL, err)
	}
	q := u.Query()
	prefix := strings.Trim(u.Path, "/")
	keyPrefix := recordsDir
	if prefix != "" {
		keyPrefix = prefix + "/" + recordsDir
	}

	switch u.Scheme {
	case "file":
		dir := strings.TrimPrefix(inner, "file://")
		if i := strings.IndexByte(dir, '?'); i >= 0 {
			dir = dir[:i]
		}
		if dir == "~" || strings.HasPrefix(dir, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("plunge: resolving ~ in %q: %w", plungeURL, err)
			}
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
		return staterecord.NewLocalStore(filepath.Join(dir, filepath.FromSlash(recordsDir)))

	case "s3":
		var opts []func(*awsconfig.LoadOptions) error
		if r := q.Get("region"); r != "" {
			opts = append(opts, awsconfig.WithRegion(r))
		}
		if p := q.Get("profile"); p != "" {
			opts = append(opts, awsconfig.WithSharedConfigProfile(p))
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("plunge: loading the AWS configuration for %q: %w", plungeURL, err)
		}
		client := s3.NewFromConfig(cfg, func(o *s3.Options) {
			if e := q.Get("endpoint"); e != "" {
				o.BaseEndpoint = aws.String(e)
			}
			if q.Get("s3ForcePathStyle") == "true" || q.Get("use_path_style") == "true" {
				o.UsePathStyle = true
			}
		})
		// The bucket's settings are checked on open, as the Azure Blob store's are (INTENTIUS/plunge#20):
		// versioning, a lifecycle rule for noncurrent versions, and the public access block. bucket_owner, when
		// set, makes every request carry S3's ExpectedBucketOwner.
		store, err := staterecord.NewS3Store(staterecord.S3Config{
			Client: client, Bucket: u.Host, KeyPrefix: keyPrefix, ExpectedBucketOwner: q.Get("bucket_owner"),
		})
		if err != nil {
			return nil, err
		}
		if err := enforceContract(ctx, store, allowInsecure(q)); err != nil {
			return nil, err
		}
		return store, nil

	case "azblob":
		account := q.Get("storage_account")
		if account == "" {
			account = os.Getenv("AZURE_STORAGE_ACCOUNT")
		}
		if account == "" {
			return nil, fmt.Errorf("plunge: %q names no storage account: set the storage_account query key "+
				"or AZURE_STORAGE_ACCOUNT", plungeURL)
		}
		resourceGroup := q.Get("resource_group")
		if resourceGroup == "" {
			resourceGroup = os.Getenv("AZURE_RESOURCE_GROUP")
		}
		subscription := q.Get("subscription_id")
		if subscription == "" {
			subscription = os.Getenv("AZURE_SUBSCRIPTION_ID")
		}
		var client *container.Client
		var factory *armstorage.ClientFactory
		unavailable := ""
		if hasAzureCustomEndpoint(q) {
			// An emulator's endpoints: never real Azure, never the shell's login (INTENTIUS/plunge#35).
			client, factory, unavailable, err = azureCustomStore(ctx, plungeURL, account, u.Host, subscription, q)
			if err != nil {
				return nil, err
			}
		} else {
			cred, err := azidentity.NewDefaultAzureCredential(nil)
			if err != nil {
				return nil, fmt.Errorf("plunge: building an Azure credential for %q: %w", plungeURL, err)
			}
			endpoint := fmt.Sprintf("https://%s.blob.core.windows.net/%s", account, u.Host)
			client, err = container.NewClient(endpoint, cred, nil)
			if err != nil {
				return nil, fmt.Errorf("plunge: building the container client for %q: %w", plungeURL, err)
			}
			if resourceGroup != "" && subscription != "" {
				factory, err = armstorage.NewClientFactory(subscription, cred, nil)
				if err != nil {
					return nil, fmt.Errorf("plunge: building the storage management client for %q: %w", plungeURL, err)
				}
			}
		}
		cfg := staterecord.AzureBlobConfig{
			Container:     client,
			Account:       account,
			ContainerName: u.Host,
			ResourceGroup: resourceGroup,
			KeyPrefix:     keyPrefix,
		}
		switch {
		case unavailable != "":
			cfg.ContractUnavailable = unavailable
		case resourceGroup == "" || subscription == "":
			// Without them the account's settings cannot be read, which the contract reports as unreadable
			// for every setting, and the open below is refused naming each.
			cfg.ContractUnavailable = fmt.Sprintf("the account's settings are read through ARM, which needs its "+
				"resource group and subscription: set the resource_group and subscription_id query keys of %q "+
				"(or AZURE_RESOURCE_GROUP and AZURE_SUBSCRIPTION_ID)", plungeURL)
		default:
			cfg.Contract = staterecord.NewAzureContractAPI(factory, resourceGroup, account, u.Host)
		}
		store, err := staterecord.NewAzureBlobStore(cfg)
		if err != nil {
			return nil, err
		}
		if err := enforceContract(ctx, store, allowInsecure(q)); err != nil {
			return nil, err
		}
		return store, nil

	case "gs":
		client, err := storage.NewClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("plunge: building the GCS client for %q: %w", plungeURL, err)
		}
		// The bucket's settings are checked on open, as the Azure Blob store's are (INTENTIUS/plunge#20): object
		// versioning, a lifecycle rule for noncurrent versions, public access prevention and uniform bucket-level
		// access are refused when they fail; list_isolation only warns.
		store, err := staterecord.NewGCSStore(staterecord.GCSConfig{Client: client, Bucket: u.Host, KeyPrefix: keyPrefix})
		if err != nil {
			return nil, err
		}
		if err := enforceContract(ctx, store, allowInsecure(q)); err != nil {
			return nil, err
		}
		return store, nil

	case k8sScheme:
		return openKubernetesStore(plungeURL, u.Host, prefix, q)
	}
	return nil, fmt.Errorf("plunge: %q is not a plunge backend URL (want %sfile://, %ss3://, %sazblob://, %sgs:// "+
		"or %sk8s://)", plungeURL, URLPrefix, URLPrefix, URLPrefix, URLPrefix, URLPrefix)
}

// openKubernetesStore opens the Kubernetes record store a plunge+k8s://namespace/prefix URL names: one Secret per
// record in the namespace, each carrying the estate label choudoufu's estate-boundary admission policy fences writes
// with. The cluster connection is the kubeconfig (the kubeconfig query key, else KUBECONFIG and the usual
// variables), with the context query key, or the in-cluster configuration when in_cluster=true. The estate label is
// the estate query key, else the namespace.
func openKubernetesStore(plungeURL, namespace, prefix string, q url.Values) (staterecord.Store, error) {
	if namespace == "" {
		return nil, fmt.Errorf("plunge: %q names no namespace: write %s%s://<namespace>[/<prefix>]",
			plungeURL, URLPrefix, k8sScheme)
	}
	cfg, err := kubesweep.RestConfig(kubesweep.Attrs{
		InCluster:     q.Get("in_cluster") == "true",
		ConfigPath:    q.Get("kubeconfig"),
		ConfigContext: q.Get("context"),
	})
	if err != nil {
		return nil, fmt.Errorf("plunge: building the cluster connection for %q: %w", plungeURL, err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("plunge: building the Kubernetes client for %q: %w", plungeURL, err)
	}
	estate := q.Get("estate")
	if estate == "" {
		estate = namespace
	}
	// The records sit under the prefix, as on the bucket stores, so one namespace can hold several roots' records.
	keyPrefix := recordsDir
	if prefix != "" {
		keyPrefix = prefix + "/" + recordsDir
	}
	return staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:       clientset.CoreV1().Secrets(namespace),
		Clientset:     clientset,
		InsecureTLS:   cfg.Insecure,
		APIServerHost: cfg.Host,
		Namespace:     namespace,
		KeyPrefix:     keyPrefix,
		Estate:        estate,
	})
}

// ContractRefusedError refuses to open a record store whose own settings fail the contract its records depend on
// (staterecord's ContractChecker): versioning, a lifecycle rule for previous versions and no anonymous access, on
// S3, GCS and Azure Blob alike. It names each failed setting with the store's own words and the fix. A setting named
// in allow_insecure (or PLUNGE_ALLOW_INSECURE) is proceeded past, except one the store marks unwaivable.
type ContractRefusedError struct {
	Subject  string
	Settings []staterecord.Setting
	Text     string
}

func (e *ContractRefusedError) Error() string {
	names := make([]string, len(e.Settings))
	for i, s := range e.Settings {
		names[i] = string(s)
	}
	return fmt.Sprintf("plunge: refusing to open the record store %s: it fails %s.\n\n%s\n\nTo proceed past a "+
		"setting anyway, name it in the URL's allow_insecure query key or in %s",
		e.Subject, strings.Join(names, ", "), e.Text, EnvAllowInsecure)
}

// enforceContract runs a store's contract check, when it has one, and refuses by name a store that fails it.
func enforceContract(ctx context.Context, store staterecord.Store, waived []string) error {
	checker, ok := staterecord.AsContractChecker(store)
	if !ok {
		return nil
	}
	label, value := checker.ContractSubject()
	subject := label + " " + value
	findings, err := checker.CheckContract(ctx, staterecord.ContractOptions{Namespaces: []string{"stacks/"}})
	if err != nil {
		summary, detail := checker.ContractCheckFailed(err)
		return fmt.Errorf("plunge: %s: %s: %w", summary, detail, err)
	}
	refused, _, _ := staterecord.SplitWaived(findings, waived)
	if len(refused) == 0 {
		return nil
	}
	settings := make([]staterecord.Setting, len(refused))
	for i, f := range refused {
		settings[i] = f.Setting
	}
	return &ContractRefusedError{
		Subject:  subject,
		Settings: settings,
		Text:     staterecord.ContractRefusalText(checker, refused),
	}
}
