/*
Copyright © 2020 Bob Callaway <bcallawa@redhat.com>

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
package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
	"github.com/projectrekor/rekor/pkg/generated/models"
	rekord_v001 "github.com/projectrekor/rekor/pkg/types/rekord/v0.0.1"
	rpm_v001 "github.com/projectrekor/rekor/pkg/types/rpm/v0.0.1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func addSearchPFlags(cmd *cobra.Command) error {
	cmd.Flags().Var(&pkiFormatFlag{value: "pgp"}, "pki-format", "format of the signature and/or public key")

	cmd.Flags().Var(&fileOrURLFlag{}, "public-key", "path or URL to public key file")

	cmd.Flags().Var(&fileOrURLFlag{}, "artifact", "path or URL to artifact file")

	cmd.Flags().Var(&shaFlag{}, "sha", "the sha of the artifact")
	return nil
}

func validateSearchPFlags() error {
	artifactStr := viper.GetString("artifact")

	publicKey := viper.GetString("public-key")
	sha := viper.GetString("sha")

	if artifactStr == "" && publicKey == "" && sha == "" {
		return errors.New("either 'sha' or 'artifact' or 'public-key' must be specified")
	}
	if publicKey != "" {
		if viper.GetString("pki-format") == "" {
			return errors.New("pki-format must be specified if searching by public-key")
		}
	}
	return nil
}

func addArtifactPFlags(cmd *cobra.Command) error {
	cmd.Flags().Var(&fileOrURLFlag{}, "signature", "path or URL to detached signature file")
	cmd.Flags().Var(&typeFlag{value: "rekord"}, "type", "type of entry")
	cmd.Flags().Var(&pkiFormatFlag{value: "pgp"}, "pki-format", "format of the signature and/or public key")

	cmd.Flags().Var(&fileOrURLFlag{}, "public-key", "path or URL to public key file")

	cmd.Flags().Var(&fileOrURLFlag{}, "artifact", "path or URL to artifact file")

	cmd.Flags().Var(&fileOrURLFlag{}, "entry", "path or URL to pre-formatted entry file")

	cmd.Flags().Var(&shaFlag{}, "sha", "the sha of the artifact")
	return nil
}

func validateArtifactPFlags(uuidValid, indexValid bool) error {
	uuidGiven := false
	if uuidValid {
		uuid := shaFlag{}
		uuidStr := viper.GetString("uuid")

		if uuidStr != "" {
			if err := uuid.Set(uuidStr); err != nil {
				return err
			}
			uuidGiven = true
		}
	}
	indexGiven := false
	if indexValid {
		logIndex := logIndexFlag{}
		logIndexStr := viper.GetString("log-index")

		if logIndexStr != "" {
			if err := logIndex.Set(logIndexStr); err != nil {
				return err
			}
			indexGiven = true
		}
	}
	// we will need artifact, public-key, signature, and potentially SHA
	typeStr := viper.GetString("type")
	entry := viper.GetString("entry")

	artifact := fileOrURLFlag{}
	artifactStr := viper.GetString("artifact")
	if artifactStr != "" {
		if err := artifact.Set(artifactStr); err != nil {
			return err
		}
	}

	signature := viper.GetString("signature")
	publicKey := viper.GetString("public-key")
	sha := viper.GetString("sha")

	if entry == "" && artifact.String() == "" {
		if (uuidGiven && uuidValid) || (indexGiven && indexValid) {
			return nil
		}
		return errors.New("either 'entry' or 'artifact' must be specified")
	}

	if entry == "" {
		if artifact.IsURL && sha == "" {
			return errors.New("a valid SHA hash must be specified when specifying a URL for --artifact")
		}
		if signature == "" && typeStr == "rekord" {
			return errors.New("--signature is required when --artifact is used")
		}
		if publicKey == "" {
			return errors.New("--public-key is required when --artifact is used")
		}
	}

	return nil
}

func CreateRpmFromPFlags() (models.ProposedEntry, error) {
	//TODO: how to select version of item to create
	returnVal := models.Rpm{}
	re := new(rpm_v001.V001Entry)

	rpm := viper.GetString("entry")
	if rpm != "" {
		var rpmBytes []byte
		rpmURL, err := url.Parse(rpm)
		if err == nil && rpmURL.IsAbs() {
			/* #nosec G107 */
			rpmResp, err := http.Get(rpm)
			if err != nil {
				return nil, fmt.Errorf("error fetching 'rpm': %w", err)
			}
			defer rpmResp.Body.Close()
			rpmBytes, err = ioutil.ReadAll(rpmResp.Body)
			if err != nil {
				return nil, fmt.Errorf("error fetching 'rpm': %w", err)
			}
		} else {
			rpmBytes, err = ioutil.ReadFile(filepath.Clean(rpm))
			if err != nil {
				return nil, fmt.Errorf("error processing 'rpm' file: %w", err)
			}
		}
		if err := json.Unmarshal(rpmBytes, &returnVal); err != nil {
			return nil, fmt.Errorf("error parsing rpm file: %w", err)
		}
	} else {
		// we will need artifact, public-key, signature, and potentially SHA
		re.RPMModel = models.RpmV001Schema{}
		re.RPMModel.Package = &models.RpmV001SchemaPackage{}

		artifact := viper.GetString("artifact")
		dataURL, err := url.Parse(artifact)
		if err == nil && dataURL.IsAbs() {
			re.RPMModel.Package.URL = strfmt.URI(artifact)
			re.RPMModel.Package.Hash = &models.RpmV001SchemaPackageHash{}
			re.RPMModel.Package.Hash.Algorithm = swag.String(models.RpmV001SchemaPackageHashAlgorithmSha256)
			re.RPMModel.Package.Hash.Value = swag.String(viper.GetString("sha"))
		} else {
			artifactBytes, err := ioutil.ReadFile(filepath.Clean(artifact))
			if err != nil {
				return nil, fmt.Errorf("error reading artifact file: %w", err)
			}
			re.RPMModel.Package.Content = strfmt.Base64(artifactBytes)
		}

		re.RPMModel.PublicKey = &models.RpmV001SchemaPublicKey{}
		publicKey := viper.GetString("public-key")
		keyURL, err := url.Parse(publicKey)
		if err == nil && keyURL.IsAbs() {
			re.RPMModel.PublicKey.URL = strfmt.URI(publicKey)
		} else {
			keyBytes, err := ioutil.ReadFile(filepath.Clean(publicKey))
			if err != nil {
				return nil, fmt.Errorf("error reading public key file: %w", err)
			}
			re.RPMModel.PublicKey.Content = strfmt.Base64(keyBytes)
		}

		if err := re.Validate(); err != nil {
			return nil, err
		}

		if re.HasExternalEntities() {
			if err := re.FetchExternalEntities(context.Background()); err != nil {
				return nil, fmt.Errorf("error retrieving external entities: %v", err)
			}
		}

		returnVal.APIVersion = swag.String(re.APIVersion())
		returnVal.Spec = re.RPMModel
	}

	return &returnVal, nil
}

func CreateRekordFromPFlags() (models.ProposedEntry, error) {
	//TODO: how to select version of item to create
	returnVal := models.Rekord{}
	re := new(rekord_v001.V001Entry)

	rekord := viper.GetString("entry")
	if rekord != "" {
		var rekordBytes []byte
		rekordURL, err := url.Parse(rekord)
		if err == nil && rekordURL.IsAbs() {
			/* #nosec G107 */
			rekordResp, err := http.Get(rekord)
			if err != nil {
				return nil, fmt.Errorf("error fetching 'rekord': %w", err)
			}
			defer rekordResp.Body.Close()
			rekordBytes, err = ioutil.ReadAll(rekordResp.Body)
			if err != nil {
				return nil, fmt.Errorf("error fetching 'rekord': %w", err)
			}
		} else {
			rekordBytes, err = ioutil.ReadFile(filepath.Clean(rekord))
			if err != nil {
				return nil, fmt.Errorf("error processing 'rekord' file: %w", err)
			}
		}
		if err := json.Unmarshal(rekordBytes, &returnVal); err != nil {
			return nil, fmt.Errorf("error parsing rekord file: %w", err)
		}
	} else {
		// we will need artifact, public-key, signature, and potentially SHA
		re.RekordObj.Data = &models.RekordV001SchemaData{}

		artifact := viper.GetString("artifact")
		dataURL, err := url.Parse(artifact)
		if err == nil && dataURL.IsAbs() {
			re.RekordObj.Data.URL = strfmt.URI(artifact)
			re.RekordObj.Data.Hash = &models.RekordV001SchemaDataHash{}
			re.RekordObj.Data.Hash.Algorithm = swag.String("sha256")
			re.RekordObj.Data.Hash.Value = swag.String(viper.GetString("sha"))
		} else {
			artifactBytes, err := ioutil.ReadFile(filepath.Clean(artifact))
			if err != nil {
				return nil, fmt.Errorf("error reading artifact file: %w", err)
			}
			re.RekordObj.Data.Content = strfmt.Base64(artifactBytes)
		}

		re.RekordObj.Signature = &models.RekordV001SchemaSignature{}
		pkiFormat := viper.GetString("pki-format")
		switch pkiFormat {
		case "pgp":
			re.RekordObj.Signature.Format = models.RekordV001SchemaSignatureFormatPgp
		case "minisign":
			re.RekordObj.Signature.Format = models.RekordV001SchemaSignatureFormatMinisign
		case "x509":
			re.RekordObj.Signature.Format = models.RekordV001SchemaSignatureFormatX509
		}
		signature := viper.GetString("signature")
		sigURL, err := url.Parse(signature)
		if err == nil && sigURL.IsAbs() {
			re.RekordObj.Signature.URL = strfmt.URI(signature)
		} else {
			signatureBytes, err := ioutil.ReadFile(filepath.Clean(signature))
			if err != nil {
				return nil, fmt.Errorf("error reading signature file: %w", err)
			}
			re.RekordObj.Signature.Content = strfmt.Base64(signatureBytes)
		}

		re.RekordObj.Signature.PublicKey = &models.RekordV001SchemaSignaturePublicKey{}
		publicKey := viper.GetString("public-key")
		keyURL, err := url.Parse(publicKey)
		if err == nil && keyURL.IsAbs() {
			re.RekordObj.Signature.PublicKey.URL = strfmt.URI(publicKey)
		} else {
			keyBytes, err := ioutil.ReadFile(filepath.Clean(publicKey))
			if err != nil {
				return nil, fmt.Errorf("error reading public key file: %w", err)
			}
			re.RekordObj.Signature.PublicKey.Content = strfmt.Base64(keyBytes)
		}

		if err := re.Validate(); err != nil {
			return nil, err
		}

		if re.HasExternalEntities() {
			if err := re.FetchExternalEntities(context.Background()); err != nil {
				return nil, fmt.Errorf("error retrieving external entities: %v", err)
			}
		}

		returnVal.APIVersion = swag.String(re.APIVersion())
		returnVal.Spec = re.RekordObj
	}

	return &returnVal, nil
}

type fileOrURLFlag struct {
	value string
	IsURL bool
}

func (f *fileOrURLFlag) String() string {
	return f.value
}

func (f *fileOrURLFlag) Set(s string) error {
	if s == "" {
		return errors.New("flag must be specified")
	}
	if _, err := os.Stat(filepath.Clean(s)); os.IsNotExist(err) {
		u := urlFlag{}
		if err := u.Set(s); err != nil {
			return err
		}
		f.IsURL = true
	}
	f.value = s
	return nil
}

func (f *fileOrURLFlag) Type() string {
	return "fileOrURLFlag"
}

type typeFlag struct {
	value string
}

func (t *typeFlag) Type() string {
	return "typeFormat"
}

func (t *typeFlag) String() string {
	return t.value
}

func (t *typeFlag) Set(s string) error {
	set := map[string]struct{}{
		"rekord": {},
		"rpm":    {},
	}
	if _, ok := set[s]; ok {
		t.value = s
		return nil
	}
	return fmt.Errorf("value specified is invalid: [%s] supported values are: [rekord, rpm]", s)
}

type pkiFormatFlag struct {
	value string
}

func (f *pkiFormatFlag) Type() string {
	return "pkiFormat"
}

func (f *pkiFormatFlag) String() string {
	return f.value
}

func (f *pkiFormatFlag) Set(s string) error {
	set := map[string]struct{}{
		"pgp":      {},
		"minisign": {},
		"x509":     {},
	}
	if _, ok := set[s]; ok {
		f.value = s
		return nil
	}
	return fmt.Errorf("value specified is invalid: [%s] supported values are: [pgp, minisign, x509]", s)
}

type shaFlag struct {
	hash string
}

func (s *shaFlag) String() string {
	return s.hash
}

func (s *shaFlag) Set(v string) error {
	if v == "" {
		return errors.New("flag must be specified")
	}
	if _, err := hex.DecodeString(v); (err != nil) || (len(v) != 64) {
		if err == nil {
			err = errors.New("invalid length for value")
		}
		return fmt.Errorf("value specified is invalid: %w", err)
	}
	s.hash = v
	return nil
}

func (s *shaFlag) Type() string {
	return "sha"
}

func addUUIDPFlags(cmd *cobra.Command, required bool) error {
	cmd.Flags().Var(&shaFlag{}, "uuid", "UUID of entry in transparency log (if known)")
	if required {
		if err := cmd.MarkFlagRequired("uuid"); err != nil {
			return err
		}
	}
	return nil
}

type logIndexFlag struct {
	index string
}

func (l *logIndexFlag) String() string {
	return l.index
}

func (l *logIndexFlag) Set(v string) error {
	if v == "" {
		return errors.New("flag must be specified")
	}
	logIndexInt, err := strconv.ParseInt(v, 10, 0)
	if err != nil {
		return fmt.Errorf("error parsing --log-index: %w", err)
	} else if logIndexInt < 0 {
		return errors.New("--log-index must be greater than or equal to 0")
	}
	l.index = v
	return nil
}

func (l *logIndexFlag) Type() string {
	return "logIndex"
}

func addLogIndexFlag(cmd *cobra.Command, required bool) error {
	cmd.Flags().Var(&logIndexFlag{}, "log-index", "the index of the entry in the transparency log")
	if required {
		if err := cmd.MarkFlagRequired("log-index"); err != nil {
			return err
		}
	}
	return nil
}
