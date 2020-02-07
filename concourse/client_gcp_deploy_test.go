package concourse_test

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/EngineerBetter/control-tower/bosh"
	"github.com/EngineerBetter/control-tower/bosh/boshfakes"
	"github.com/EngineerBetter/control-tower/commands/deploy"
	"github.com/EngineerBetter/control-tower/concourse"
	"github.com/EngineerBetter/control-tower/concourse/concoursefakes"
	"github.com/EngineerBetter/control-tower/config"
	"github.com/EngineerBetter/control-tower/config/configfakes"
	"github.com/EngineerBetter/control-tower/fly"
	"github.com/EngineerBetter/control-tower/fly/flyfakes"
	"github.com/EngineerBetter/control-tower/iaas"
	"github.com/EngineerBetter/control-tower/iaas/iaasfakes"
	"github.com/EngineerBetter/control-tower/terraform"
	"github.com/EngineerBetter/control-tower/terraform/terraformfakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	. "github.com/tjarratt/gcounterfeiter"
)

var _ = Describe("client", func() {
	var stdout *gbytes.Buffer
	var stderr *gbytes.Buffer
	var args *deploy.Args
	var configInBucket config.Config
	var terraformOutputs terraform.GCPOutputs

	var directorStateFixture, directorCredsFixture []byte

	var buildClient func() concourse.IClient
	var buildClientOtherRegion func() concourse.IClient
	var ipChecker func() (string, error)
	var tfInputVarsFactory *concoursefakes.FakeTFInputVarsFactory
	var flyClient *flyfakes.FakeIClient
	var terraformCLI *terraformfakes.FakeCLIInterface
	var configClient *configfakes.FakeIClient
	var boshClient *boshfakes.FakeIClient

	var setupFakeGcpProvider = func() *iaasfakes.FakeProvider {
		provider := &iaasfakes.FakeProvider{}
		provider.DBTypeStub = func(size string) string {
			return "db.t2." + size
		}
		provider.RegionReturns("europe-west1")
		provider.ZoneReturns("europe-west1-b")
		provider.IAASReturns(iaas.GCP)
		provider.CheckForWhitelistedIPStub = func(ip, securityGroup string) (bool, error) {
			if ip == "1.2.3.4" {
				return false, nil
			}
			return true, nil
		}
		provider.FindLongestMatchingHostedZoneStub = func(subdomain string) (string, string, error) {
			if subdomain == "ci.google.com" {
				return "google.com", "ABC123", nil
			}

			return "", "", errors.New("hosted zone not found")
		}
		return provider
	}

	var setupFakeOtherRegionProvider = func() *iaasfakes.FakeProvider {
		otherRegionClient := &iaasfakes.FakeProvider{}
		otherRegionClient.IAASReturns(iaas.GCP)
		otherRegionClient.RegionReturns("europe-west2")
		return otherRegionClient
	}

	var setupFakeTfInputVarsFactory = func() *concoursefakes.FakeTFInputVarsFactory {
		tfInputVarsFactory = &concoursefakes.FakeTFInputVarsFactory{}

		provider, err := iaas.New(iaas.GCP, "europe-west1")
		Expect(err).ToNot(HaveOccurred())
		gcpInputVarsFactory, err := concourse.NewTFInputVarsFactory(provider)
		Expect(err).ToNot(HaveOccurred())
		tfInputVarsFactory.NewInputVarsStub = func(i config.ConfigView) terraform.InputVars {
			return gcpInputVarsFactory.NewInputVars(i)
		}
		return tfInputVarsFactory
	}

	var setupFakeTerraformCLI = func(terraformOutputs terraform.GCPOutputs) *terraformfakes.FakeCLIInterface {
		terraformCLI = &terraformfakes.FakeCLIInterface{}
		terraformCLI.BuildOutputReturns(&terraformOutputs, nil)
		return terraformCLI
	}

	BeforeEach(func() {
		var err error

		json := `{"project_id": "gcp-project", "type": "service_account"}`
		filePath, err := ioutil.TempFile("", "")
		Expect(err).ToNot(HaveOccurred())
		_, err = filePath.WriteString(json)
		Expect(err).ToNot(HaveOccurred())
		filePath.Close()
		err = os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filePath.Name())
		Expect(err).ToNot(HaveOccurred())

		directorStateFixture, err = ioutil.ReadFile("fixtures/director-state.json")
		Expect(err).ToNot(HaveOccurred())
		directorCredsFixture, err = ioutil.ReadFile("fixtures/director-creds.yml")
		Expect(err).ToNot(HaveOccurred())

		//At the time of writing, these are defaults from the CLI flags
		args = &deploy.Args{
			AllowIPs:         "0.0.0.0/0",
			AllowIPsIsSet:    false,
			DBSize:           "small",
			DBSizeIsSet:      false,
			IAAS:             "GCP",
			IAASIsSet:        false,
			Spot:             true,
			SpotIsSet:        false,
			WebSize:          "small",
			WebSizeIsSet:     false,
			WorkerCount:      1,
			WorkerCountIsSet: false,
			WorkerSize:       "xlarge",
			WorkerSizeIsSet:  false,
			WorkerType:       "m4",
			WorkerTypeIsSet:  false,
		}

		terraformOutputs = terraform.GCPOutputs{
			ATCPublicIP:   terraform.MetadataStringValue{Value: "77.77.77.77"},
			BoshDBAddress: terraform.MetadataStringValue{Value: "rds.aws.com"},
			DBName:        terraform.MetadataStringValue{Value: "bosh-foo"},
			DirectorAccountCreds: terraform.MetadataStringValue{Value: `{
				"type": "service_account",
				"project_id": "control-tower-foo",
				"private_key_id": "4738698f315fc4d268",
				"private_key": "-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCgKZ28OqflRjm+\n",
				"client_email": "control-tower-cs-control-bosh@control-tower-foo.iam.gserviceaccount.com",
				"client_id": "109135524897347086276",
				"auth_uri": "https://accounts.google.com/o/oauth2/auth",
				"token_uri": "https://oauth2.googleapis.com/token",
				"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
				"client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/control-tower-cs-control-bosh%40control-tower-foo.iam.gserviceaccount.com"
			}`},
			DirectorPublicIP:            terraform.MetadataStringValue{Value: "99.99.99.99"},
			DirectorSecurityGroupID:     terraform.MetadataStringValue{Value: "sg-123"},
			NatGatewayIP:                terraform.MetadataStringValue{Value: "88.88.88.88"},
			Network:                     terraform.MetadataStringValue{Value: "control-tower-foo"},
			PrivateSubnetworkInternalGw: terraform.MetadataStringValue{Value: "10.0.1.1"},
			PrivateSubnetworkName:       terraform.MetadataStringValue{Value: "control-tower-foo-europe-west1-private"},
			PublicSubnetworkInternalGw:  terraform.MetadataStringValue{Value: "10.0.0.1"},
			PublicSubnetworkName:        terraform.MetadataStringValue{Value: "control-tower-foo-europe-west1-public"},
			SQLServerCert: terraform.MetadataStringValue{Value: `-----BEGIN CERTIFICATE-----
			MIIDfzCCAmegAwIBAgIBADANBgkqhkiG9w0BAQsFADB3MS0wKwYDVQQuEyQzY2Nl
			-----END CERTIFICATE-----`},
		}

		// Initial config in bucket from an existing deployment
		configInBucket = config.Config{
			AvailabilityZone:         "europe-west1a",
			ConcoursePassword:        "s3cret",
			ConcourseUsername:        "admin",
			ConcourseWebSize:         "medium",
			ConcourseWorkerCount:     1,
			ConcourseWorkerSize:      "large",
			Deployment:               "control-tower-happymeal",
			DirectorHMUserPassword:   "original-password",
			DirectorMbusPassword:     "original-password",
			DirectorNATSPassword:     "original-password",
			DirectorPassword:         "secret123",
			DirectorRegistryPassword: "original-password",
			DirectorUsername:         "admin",
			EncryptionKey:            "123456789a123456789b123456789c",
			IAAS:                     "GCP",
			PrivateKey: `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA2spClkDkFfy2c91Z7N3AImPf0v3o5OoqXUS6nE2NbV2bP/o7
Oa3KnpzeQ5DBmW3EW7tuvA4bAHxPuk25T9tM8jiItg0TNtMlxzFYVxFq8jMmokEi
sMVbjh9XIZptyZHbZzsJsbaP/xOGHSQNYwH/7qnszbPKN82zGwrsbrGh1hRMATbU
S+oor1XTLWGKuLs72jWJK864RW/WiN8eNfk7on1Ugqep4hnXLQjrgbOOxeX7/Pap
VEExC63c1FmZjLnOc6mLbZR07qM9jj5fmR94DzcliF8SXIvp6ERDMYtnI7gAC4XA
ZgATsS0rkb5t7dxsaUl0pHfU9HlhbMciN3bJrwIDAQABAoIBADQIWiGluRjJixKv
F83PRvxmyDpDjHm0fvLDf6Xgg7v4wQ1ME326KS/jmrBy4rf8dPBj+QfcSuuopMVn
6qRlQT1x2IGDRoiJWriusZWzXL3REGUSHI/xv75jEbO6KFYBzC4Wyk1rX3+IQyL3
Cf/738QAwYKCOZtf3jKWPHhu4lAo/rq6FY/okWMybaAXajCTF2MgJcmMm73jIgk2
6A6k9Cobs7XXNZVogAUsHU7bgnkfxYgz34UTZu0FDQRGf3MpHeWp32dhw9UAaFz7
nfoBVxU1ppqM4TCdXvezKgi8QV6imvDyD67/JNUn0B06LKMbAIK/mffA9UL8CXkc
YSj5AIECgYEA/b9MVy//iggMAh+DZf8P+fS79bblVamdHsU8GvHEDdIg0lhBl3pQ
Nrpi63sXVIMz52BONKLJ/c5/wh7xIiApOMcu2u+2VjN00dqpivasERf0WbgSdvMS
Gi+0ofG0kF94W7z8Z1o9rT4Wn9wxuqkRLLp3A5CkpjzlEnPVoW9X2I8CgYEA3LuD
ZpL2dRG5sLA6ahrJDZASk4cBaQGcYpx/N93dB3XlCTguPIJL0hbt1cwwhgCQh6cu
B0mDWsiQIMwET7bL5PX37c1QBh0rPqQsz8/T7jNEDCnbWDWQSaR8z6sGJCWEkWzo
AtzvPkTj75bDsYG0KVlYMfNJyYHZJ5ECJ08ZTOECgYEA5rLF9X7uFdC7GjMMg+8h
119qhDuExh0vfIpV2ylz1hz1OkiDWfUaeKd8yBthWrTuu64TbEeU3eyguxzmnuAe
mkB9mQ/X9wdRbnofKviZ9/CPeAKixwK3spcs4w+d2qTyCHYKBO1GpfuNFkpb7BlK
RCBDlDotd/ZlTiGCWQOiGoECgYEAmM/sQUf+/b8+ubbXSfuvMweKBL5TWJn35UEI
xemACpkw7fgJ8nQV/6VGFFxfP3YGmRNBR2Q6XtA5D6uOVI1tjN5IPUaFXyY0eRJ5
v4jW5LJzKqSTqPa0JHeOvMpe3wlmRLOLz+eabZaN4qGSa0IrMvEaoMIYVDvj1YOL
ZSFal6ECgYBDXbrmvF+G5HoASez0WpgrHxf3oZh+gP40rzwc94m9rVP28i8xTvT9
5SrvtzwjMsmQPUM/ttaBnNj1PvmOTTmRhXVw5ztAN9hhuIwVm8+mECFObq95NIgm
sWbB3FCIsym1FXB+eRnVF3Y15RwBWWKA5RfwUNpEXFxtv24tQ8jrdA==
-----END RSA PRIVATE KEY-----`,
			Project:                "happymeal",
			PublicKey:              "example-public-key",
			RDSDefaultDatabaseName: "bosh_abcdefgh",
			RDSInstanceClass:       "db-g1-small",
			RDSPassword:            "s3cret",
			RDSUsername:            "admin",
			Region:                 "europe-west1",
			Spot:                   true,
			TFStatePath:            "example-path",
			//These come from fixtures/director-creds.yml
			CredhubUsername:          "credhub-cli",
			CredhubPassword:          "f4b12bc0166cad1bc02b050e4e79ac4c",
			CredhubAdminClientSecret: "hxfgb56zny2yys6m9wjx",
			CredhubCACert:            "-----BEGIN CERTIFICATE-----\nMIIEXTCCAsWgAwIBAgIQSmhcetyHDHLOYGaqMnJ0QTANBgkqhkiG9w0BAQsFADA4\nMQwwCgYDVQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMM\nB2Jvc2hfY2EwHhcNMTkwMjEzMTAyNTM0WhcNMjAwMjEzMTAyNTM0WjA4MQwwCgYD\nVQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMMB2Jvc2hf\nY2EwggGiMA0GCSqGSIb3DQEBAQUAA4IBjwAwggGKAoIBgQC+0bA9T4awlJYSn6aq\nun6Hylu47b2UiZpFZpvPomKWPay86QaJ0vC9SK8keoYI4gWwsZSAMXp2mSCkXKRi\n+rVc+sKnzv9VgPoVY5eYIYCtJvl7KCJQE02dGoxuGOaWlBiHuD6TzY6lI9fNxkAW\neMGR3UylJ7ET0NvgAZWS1daov2GfiKkaYUCdbY8DtfhMyFhJ381VNHwoP6xlZbSf\nTInO/2TS8xpW2BcMNhFAu9MJVtC5pDHtJtkXHXep027CkrPjtFQWpzvIMvPAtZ68\n9t46nS9Ix+RmeN3v+sawNzbZscnsslhB+m4GrpL9M8g8sbweMw9yxf241z1qkiNJ\nto3HRqqyNyGsvI9n7OUrZ4D5oAfY7ze1TF+nxnkmJp14y21FEdG7t76N0J5dn6bJ\n/lroojig/PqabRsyHbmj6g8N832PEQvwsPptihEwgrRmY6fcBbMUaPCpNuVTJVa5\ng0KdBGDYDKTMlEn4xaj8P1wRbVjtXVMED2l4K4tS/UiDIb8CAwEAAaNjMGEwDgYD\nVR0PAQH/BAQDAgEGMA8GA1UdEwEB/wQFMAMBAf8wHQYDVR0OBBYEFHii4fiqAwJS\nnNhi6C+ibr/4OOTyMB8GA1UdIwQYMBaAFHii4fiqAwJSnNhi6C+ibr/4OOTyMA0G\nCSqGSIb3DQEBCwUAA4IBgQAGXDTlsQWIJHfvU3zy9te35adKOUeDwk1lSe4NYvgW\nFJC0w2K/1ZldmQ2leHmiXSukDJAYmROy9Y1qkUazTzjsdvHGhUF2N1p7fIweNj8e\ncsR+T21MjPEwD99m5+xLvnMRMuqzH9TqVbFIM3lmCDajh8n9cp4KvGkQmB+X7DE1\nR6AXG4EN9xn91TFrqmFFNOrFtoAjtag05q/HoqMhFFVeg+JTpsPshFjlWIkzwqKx\npn68KG2ztgS0KeDraGKwItTKengTCr/VkgorXnhKcI1C6C5iRXZp3wREu8RO+wRe\nKSGbsYIHaFxd3XwW4JnsW+hes/W5MZX01wkwOLrktf85FjssBZBavxBbyFag/LvS\n8oULOZRLYUkuElM+0Wzf8ayB574Fd97gzCVzWoD0Ei982jAdbEfk77PV1TvMNmEn\n3M6ktB7GkjuD9OL12iNzxmbQe7p1WkYYps9hK4r0pbyxZPZlPMmNNZo579rywDjF\nwEW5QkylaPEkbVDhJWeR1I8=\n-----END CERTIFICATE-----\n",
			VMProvisioningType:       "spot",
			WorkerType:               "m4",
		}
	})

	JustBeforeEach(func() {

		flyClient = &flyfakes.FakeIClient{}
		gcpClient := setupFakeGcpProvider()
		otherRegionClient := setupFakeOtherRegionProvider()
		tfInputVarsFactory = setupFakeTfInputVarsFactory()
		configClient = &configfakes.FakeIClient{}
		terraformCLI = setupFakeTerraformCLI(terraformOutputs)

		boshClientFactory := func(config config.ConfigView, outputs terraform.Outputs, stdout, stderr io.Writer, provider iaas.Provider, versionFile []byte) (bosh.IClient, error) {
			boshClient = &boshfakes.FakeIClient{}
			boshClient.DeployReturns(directorStateFixture, directorCredsFixture, nil)
			return boshClient, nil
		}

		ipChecker = func() (string, error) {
			return "192.0.2.0", nil
		}

		stdout = gbytes.NewBuffer()
		stderr = gbytes.NewBuffer()

		versionFile := []byte("some versions")

		buildClient = func() concourse.IClient {
			return concourse.NewClient(
				gcpClient,
				terraformCLI,
				tfInputVarsFactory,
				boshClientFactory,
				func(iaas.Provider, fly.Credentials, io.Writer, io.Writer, []byte) (fly.IClient, error) {
					return flyClient, nil
				},
				configClient,
				args,
				stdout,
				stderr,
				ipChecker,
				func(size int) string { return fmt.Sprintf("generatedPassword%d", size) },
				func() string { return "8letters" },
				func() ([]byte, []byte, string, error) { return []byte("private"), []byte("public"), "fingerprint", nil },
				"some version",
				versionFile,
			)
		}

		buildClientOtherRegion = func() concourse.IClient {
			return concourse.NewClient(
				otherRegionClient,
				terraformCLI,
				tfInputVarsFactory,
				boshClientFactory,
				func(iaas.Provider, fly.Credentials, io.Writer, io.Writer, []byte) (fly.IClient, error) {
					return flyClient, nil
				},
				configClient,
				args,
				stdout,
				stderr,
				ipChecker,
				func(size int) string { return fmt.Sprintf("generatedPassword%d", size) },
				func() string { return "8letters" },
				func() ([]byte, []byte, string, error) { return []byte("private"), []byte("public"), "fingerprint", nil },
				"some version",
				versionFile,
			)
		}
	})

	Describe("Deploy", func() {
		Context("when there is an existing config", func() {
			var configAfterLoad, configAfterCreateEnv, configAfterConcourseDeploy config.Config
			var terraformInputVars *terraform.GCPInputVars

			Context("and no CLI args were provided", func() {
				BeforeEach(func() {
					configInBucket.ConcourseCACert = `-----BEGIN CERTIFICATE-----
MIIEXTCCAsWgAwIBAgIQZiZWMIod+NGTx+jJ8mBIbzANBgkqhkiG9w0BAQsFADA4
MQwwCgYDVQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMM
B2Jvc2hfY2EwHhcNMTkwMjEzMTAyNTM1WhcNMjAwMjEzMTAyNTM1WjAmMQwwCgYD
VQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkwggGiMA0GCSqGSIb3DQEB
AQUAA4IBjwAwggGKAoIBgQDCSub74gKqpTNFLeeEHNAH9a4Cf1ITQ11iK6OmzCM0
NloX6/o2ER23AHAXBIJLPEVX4qnQKNeQjKFcicSdTK0kVVuLa5mlFY4/ieCmXKA+
jmeXPJQGdzFi8BgoLAITcnFGGZY8kwPCzhhrPfa7TcvJnK/2RtKOwgWMxK2kxqs+
EtA2fxZb57EV05kS7ctoHfiSjAOKqWlsMGOon0z22HItuvV8hcEB85oyv9AbN6Ni
GoaktghNEz3A9T0d2iJBMX7uZgEKmq1VwhqTUAXbr+kxN23Dc1m2b7eMHZ0GyOes
Puwj6ZG9Zqypf+wyb5ndZWwxFAex6Ery02W0rBFKne9J4VRxOzy/IgJKc1bvqtjs
EpU1FbDCw6tLb9PKltEO7AQMrx70ubYuVt4exWfZVhzHNBzhII7gmLegHB0eGWou
KLWV2hDM9OgDdbfmSubqTN+7szTvlUZTAwsLiTUQMCT7JpJSjqs7jXOO59PT0qSn
W/QT8Q/BwiQrCeNAjzVhHU8CAwEAAaN1MHMwDgYDVR0PAQH/BAQDAgWgMBMGA1Ud
JQQMMAoGCCsGAQUFBwMBMAwGA1UdEwEB/wQCMAAwHQYDVR0OBBYEFMaEYqmheOXo
GU3n6SiMLazbnxh5MB8GA1UdIwQYMBaAFHii4fiqAwJSnNhi6C+ibr/4OOTyMA0G
CSqGSIb3DQEBCwUAA4IBgQCmjAuwHHby7HlvBgGGkBwqEtwX6r4rgh0XNXJVyh6J
cCvGpKzDBl+XX8a1KQ4T1f5L81TR4JeV94DUMQ9xvOD2foBa6BMjDD4rQB216HlF
sTh05eHp3TnLD7+Usu3iLQX2kYViEXdFh33xs3SD63PoKl2xS8h4PRenouaDH8Lz
QT2zsqZMAy7zTwLa6A746OVwUT2xngkpTpFFsyIrNbwtvYuF54mqCQ7lw1Rfx1KE
eXZl7o48YRh/IOuDZPjdyQqgOQeOhBqH8MLd3iWTyat/0jwd/VhIKpwxRZqlhlI+
gEjWiNnil1JTl/I1AetpHX2oACkhg67IUR0MsbGSL2/KruVlHuLZdQMSNq0wBh6H
Ni/w26gJvipll4mPV/Kr/LyWXQy2tkqhF00/fdhoZpWh93xzbaffriy+eaPqvFCc
9HeOuco3Br39wORSRNL5Gb5ARgb30Z8syPEPSkt/g93Kj7wKiiiIFp+psuiIgnxh
EWtqtr5TdtFYrxertqRY2vI=
-----END CERTIFICATE-----
`

					configInBucket.ConcourseCert = "existing Cert"
					configInBucket.ConcourseKey = "existing Key"

					//Mutations we expect to have been done after load
					configAfterLoad = configInBucket
					configAfterLoad.AllowIPs = "\"0.0.0.0/0\""
					configAfterLoad.HostedZoneID = ""
					configAfterLoad.HostedZoneRecordPrefix = ""
					configAfterLoad.SourceAccessIP = ""
					configAfterLoad.PrivateCIDR = "10.0.1.0/24"
					configAfterLoad.PublicCIDR = "10.0.0.0/24"
					configAfterLoad.SourceAccessIP = "192.0.2.0"

					terraformInputVars = &terraform.GCPInputVars{
						AllowIPs:           configAfterLoad.AllowIPs,
						ConfigBucket:       configAfterLoad.ConfigBucket,
						DBName:             configAfterLoad.RDSDefaultDatabaseName,
						DBPassword:         configAfterLoad.RDSPassword,
						DBTier:             configAfterLoad.RDSInstanceClass,
						DBUsername:         configAfterLoad.RDSUsername,
						Deployment:         configAfterLoad.Deployment,
						DNSManagedZoneName: configAfterLoad.HostedZoneID,
						DNSRecordSetPrefix: configAfterLoad.HostedZoneRecordPrefix,
						ExternalIP:         configAfterLoad.SourceAccessIP,
						GCPCredentialsJSON: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
						Namespace:          configAfterLoad.Namespace,
						PrivateCIDR:        configAfterLoad.PrivateCIDR,
						Project:            "gcp-project",
						PublicCIDR:         configAfterLoad.PublicCIDR,
						Region:             configAfterLoad.Region,
						Tags:               "",
						Zone:               "europe-west1-b",
					}

					//Mutations we expect to have been done after deploying the director
					configAfterCreateEnv = configAfterLoad
					configAfterCreateEnv.DirectorPublicIP = "99.99.99.99"
					configAfterCreateEnv.Domain = "77.77.77.77"
					configAfterCreateEnv.Tags = []string{"control-tower-version=some version"}
					configAfterCreateEnv.Version = "some version"

					// Mutations we expect to have been done after deploying Concourse
					configAfterConcourseDeploy = configAfterCreateEnv
					configAfterConcourseDeploy.CredhubAdminClientSecret = "hxfgb56zny2yys6m9wjx"
					configAfterConcourseDeploy.CredhubCACert = `-----BEGIN CERTIFICATE-----
MIIEXTCCAsWgAwIBAgIQSmhcetyHDHLOYGaqMnJ0QTANBgkqhkiG9w0BAQsFADA4
MQwwCgYDVQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMM
B2Jvc2hfY2EwHhcNMTkwMjEzMTAyNTM0WhcNMjAwMjEzMTAyNTM0WjA4MQwwCgYD
VQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMMB2Jvc2hf
Y2EwggGiMA0GCSqGSIb3DQEBAQUAA4IBjwAwggGKAoIBgQC+0bA9T4awlJYSn6aq
un6Hylu47b2UiZpFZpvPomKWPay86QaJ0vC9SK8keoYI4gWwsZSAMXp2mSCkXKRi
+rVc+sKnzv9VgPoVY5eYIYCtJvl7KCJQE02dGoxuGOaWlBiHuD6TzY6lI9fNxkAW
eMGR3UylJ7ET0NvgAZWS1daov2GfiKkaYUCdbY8DtfhMyFhJ381VNHwoP6xlZbSf
TInO/2TS8xpW2BcMNhFAu9MJVtC5pDHtJtkXHXep027CkrPjtFQWpzvIMvPAtZ68
9t46nS9Ix+RmeN3v+sawNzbZscnsslhB+m4GrpL9M8g8sbweMw9yxf241z1qkiNJ
to3HRqqyNyGsvI9n7OUrZ4D5oAfY7ze1TF+nxnkmJp14y21FEdG7t76N0J5dn6bJ
/lroojig/PqabRsyHbmj6g8N832PEQvwsPptihEwgrRmY6fcBbMUaPCpNuVTJVa5
g0KdBGDYDKTMlEn4xaj8P1wRbVjtXVMED2l4K4tS/UiDIb8CAwEAAaNjMGEwDgYD
VR0PAQH/BAQDAgEGMA8GA1UdEwEB/wQFMAMBAf8wHQYDVR0OBBYEFHii4fiqAwJS
nNhi6C+ibr/4OOTyMB8GA1UdIwQYMBaAFHii4fiqAwJSnNhi6C+ibr/4OOTyMA0G
CSqGSIb3DQEBCwUAA4IBgQAGXDTlsQWIJHfvU3zy9te35adKOUeDwk1lSe4NYvgW
FJC0w2K/1ZldmQ2leHmiXSukDJAYmROy9Y1qkUazTzjsdvHGhUF2N1p7fIweNj8e
csR+T21MjPEwD99m5+xLvnMRMuqzH9TqVbFIM3lmCDajh8n9cp4KvGkQmB+X7DE1
R6AXG4EN9xn91TFrqmFFNOrFtoAjtag05q/HoqMhFFVeg+JTpsPshFjlWIkzwqKx
pn68KG2ztgS0KeDraGKwItTKengTCr/VkgorXnhKcI1C6C5iRXZp3wREu8RO+wRe
KSGbsYIHaFxd3XwW4JnsW+hes/W5MZX01wkwOLrktf85FjssBZBavxBbyFag/LvS
8oULOZRLYUkuElM+0Wzf8ayB574Fd97gzCVzWoD0Ei982jAdbEfk77PV1TvMNmEn
3M6ktB7GkjuD9OL12iNzxmbQe7p1WkYYps9hK4r0pbyxZPZlPMmNNZo579rywDjF
wEW5QkylaPEkbVDhJWeR1I8=
-----END CERTIFICATE-----
`
					configAfterConcourseDeploy.CredhubPassword = "f4b12bc0166cad1bc02b050e4e79ac4c"
					configAfterConcourseDeploy.CredhubURL = "https://77.77.77.77:8844/"
					configAfterConcourseDeploy.CredhubUsername = "credhub-cli"
					configAfterConcourseDeploy.DirectorCACert = `-----BEGIN CERTIFICATE-----
MIIEXTCCAsWgAwIBAgIQZiZWMIod+NGTx+jJ8mBIbzANBgkqhkiG9w0BAQsFADA4
MQwwCgYDVQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMM
B2Jvc2hfY2EwHhcNMTkwMjEzMTAyNTM1WhcNMjAwMjEzMTAyNTM1WjAmMQwwCgYD
VQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkwggGiMA0GCSqGSIb3DQEB
AQUAA4IBjwAwggGKAoIBgQDCSub74gKqpTNFLeeEHNAH9a4Cf1ITQ11iK6OmzCM0
NloX6/o2ER23AHAXBIJLPEVX4qnQKNeQjKFcicSdTK0kVVuLa5mlFY4/ieCmXKA+
jmeXPJQGdzFi8BgoLAITcnFGGZY8kwPCzhhrPfa7TcvJnK/2RtKOwgWMxK2kxqs+
EtA2fxZb57EV05kS7ctoHfiSjAOKqWlsMGOon0z22HItuvV8hcEB85oyv9AbN6Ni
GoaktghNEz3A9T0d2iJBMX7uZgEKmq1VwhqTUAXbr+kxN23Dc1m2b7eMHZ0GyOes
Puwj6ZG9Zqypf+wyb5ndZWwxFAex6Ery02W0rBFKne9J4VRxOzy/IgJKc1bvqtjs
EpU1FbDCw6tLb9PKltEO7AQMrx70ubYuVt4exWfZVhzHNBzhII7gmLegHB0eGWou
KLWV2hDM9OgDdbfmSubqTN+7szTvlUZTAwsLiTUQMCT7JpJSjqs7jXOO59PT0qSn
W/QT8Q/BwiQrCeNAjzVhHU8CAwEAAaN1MHMwDgYDVR0PAQH/BAQDAgWgMBMGA1Ud
JQQMMAoGCCsGAQUFBwMBMAwGA1UdEwEB/wQCMAAwHQYDVR0OBBYEFMaEYqmheOXo
GU3n6SiMLazbnxh5MB8GA1UdIwQYMBaAFHii4fiqAwJSnNhi6C+ibr/4OOTyMA0G
CSqGSIb3DQEBCwUAA4IBgQCmjAuwHHby7HlvBgGGkBwqEtwX6r4rgh0XNXJVyh6J
cCvGpKzDBl+XX8a1KQ4T1f5L81TR4JeV94DUMQ9xvOD2foBa6BMjDD4rQB216HlF
sTh05eHp3TnLD7+Usu3iLQX2kYViEXdFh33xs3SD63PoKl2xS8h4PRenouaDH8Lz
QT2zsqZMAy7zTwLa6A746OVwUT2xngkpTpFFsyIrNbwtvYuF54mqCQ7lw1Rfx1KE
eXZl7o48YRh/IOuDZPjdyQqgOQeOhBqH8MLd3iWTyat/0jwd/VhIKpwxRZqlhlI+
gEjWiNnil1JTl/I1AetpHX2oACkhg67IUR0MsbGSL2/KruVlHuLZdQMSNq0wBh6H
Ni/w26gJvipll4mPV/Kr/LyWXQy2tkqhF00/fdhoZpWh93xzbaffriy+eaPqvFCc
9HeOuco3Br39wORSRNL5Gb5ARgb30Z8syPEPSkt/g93Kj7wKiiiIFp+psuiIgnxh
EWtqtr5TdtFYrxertqRY2vI=
-----END CERTIFICATE-----
`
				})

				JustBeforeEach(func() {
					configClient.LoadReturns(configInBucket, nil)
					configClient.ConfigExistsReturns(true, nil)
					configClient.HasAssetReturnsOnCall(0, true, nil)
					configClient.LoadAssetReturnsOnCall(0, directorStateFixture, nil)
					configClient.HasAssetReturnsOnCall(1, true, nil)
					configClient.LoadAssetReturnsOnCall(1, directorCredsFixture, nil)
				})

				It("does all the things in the right order", func() {
					client := buildClient()
					err := client.Deploy()
					Expect(err).ToNot(HaveOccurred())

					tfInputVarsFactory.NewInputVarsReturns(terraformInputVars)

					Expect(configClient).To(HaveReceived("EnsureBucketExists"))
					Expect(configClient).To(HaveReceived("ConfigExists"))
					Expect(configClient).To(HaveReceived("Load"))
					Expect(tfInputVarsFactory).To(HaveReceived("NewInputVars").With(configAfterLoad))
					Expect(terraformCLI).To(HaveReceived("Apply").With(terraformInputVars))
					Expect(terraformCLI).To(HaveReceived("BuildOutput").With(terraformInputVars))
					Expect(configClient).To(HaveReceived("Update").With(configAfterLoad))
					Expect(configClient).To(HaveReceived("HasAsset").With("director-state.json"))
					Expect(configClient.HasAssetArgsForCall(0)).To(Equal("director-state.json"))
					Expect(configClient).To(HaveReceived("LoadAsset").With("director-state.json"))
					Expect(configClient.LoadAssetArgsForCall(0)).To(Equal("director-state.json"))
					Expect(configClient).To(HaveReceived("HasAsset").With("director-creds.yml"))
					Expect(configClient.HasAssetArgsForCall(1)).To(Equal("director-creds.yml"))
					Expect(configClient).To(HaveReceived("LoadAsset").With("director-creds.yml"))
					Expect(configClient.LoadAssetArgsForCall(1)).To(Equal("director-creds.yml"))
					Expect(boshClient).To(HaveReceived("Deploy").With(directorStateFixture, directorCredsFixture, false))

					Expect(configClient).To(HaveReceived("StoreAsset").With("director-state.json", directorStateFixture))
					Expect(configClient).To(HaveReceived("StoreAsset").With("director-creds.yml", directorCredsFixture))
					Expect(boshClient).To(HaveReceived("Cleanup"))
					Expect(flyClient).To(HaveReceived("SetDefaultPipeline").With(configAfterCreateEnv, false))
					Expect(configClient).To(HaveReceived("Update").With(configAfterConcourseDeploy))
				})

				It("Warns about access to local machine", func() {
					client := buildClient()
					err := client.Deploy()
					Expect(err).ToNot(HaveOccurred())

					Eventually(stderr).Should(gbytes.Say("WARNING: allowing access from local machine"))
				})

				It("Prints the bosh credentials", func() {
					client := buildClient()
					err := client.Deploy()
					Expect(err).ToNot(HaveOccurred())
					Eventually(stdout).Should(gbytes.Say("DEPLOY SUCCESSFUL"))
					Eventually(stdout).Should(gbytes.Say("fly --target happymeal login --insecure --concourse-url https://77.77.77.77 --username admin --password s3cret"))
				})

				It("Notifies the user", func() {
					client := buildClient()
					err := client.Deploy()
					Expect(err).ToNot(HaveOccurred())

					Eventually(stdout).Should(gbytes.Say("USING PREVIOUS DEPLOYMENT CONFIG"))
				})
			})

			Context("and custom CIDR ranges were provided", func() {
				BeforeEach(func() {
					args.NetworkCIDR = "10.0.0.0/16"
					args.NetworkCIDRIsSet = true
					args.PrivateCIDR = "10.0.0.0/24"
					args.PrivateCIDRIsSet = true
					args.PublicCIDR = "10.0.1.0/24"
					args.PublicCIDRIsSet = true
					args.RDS1CIDR = "10.0.2.0/24"
					args.RDS2CIDR = "10.0.3.0/24"
				})

				JustBeforeEach(func() {
					configClient.LoadReturns(configInBucket, nil)
					configClient.ConfigExistsReturns(true, nil)
					configClient.HasAssetReturnsOnCall(0, true, nil)
					configClient.LoadAssetReturnsOnCall(0, directorStateFixture, nil)
					configClient.HasAssetReturnsOnCall(1, true, nil)
					configClient.LoadAssetReturnsOnCall(1, directorCredsFixture, nil)
				})

				It("fails with a warning about not being able to specify CIDRs after first deploy", func() {
					client := buildClient()
					err := client.Deploy()
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("custom CIDRs cannot be applied after intial deploy"))
				})
			})

			Context("and all the CLI args were provided", func() {
				BeforeEach(func() {
					// Set all changeable arguments (IE, not IAAS, Region, Namespace, AZ, et al)
					args.AllowIPs = "88.98.225.40"
					args.AllowIPsIsSet = true
					args.DBSize = "4xlarge"
					args.DBSizeIsSet = true
					args.Domain = "ci.google.com"
					args.DomainIsSet = true
					args.GithubAuthClientID = "github-client-id"
					args.GithubAuthClientIDIsSet = true
					args.GithubAuthClientSecret = "github-client-secret"
					args.GithubAuthClientSecretIsSet = true
					args.GithubAuthIsSet = true
					args.Spot = false
					args.SpotIsSet = true
					args.Tags = []string{"env=prod", "team=foo"}
					args.TagsIsSet = true
					args.TLSCert = "i-am-a-tls-cert"
					args.TLSCertIsSet = true
					args.TLSKey = "i-am-a-tls-key"
					args.TLSKeyIsSet = true
					args.WebSize = "2xlarge"
					args.WebSizeIsSet = true
					args.WorkerCount = 2
					args.WorkerCountIsSet = true
					args.WorkerSize = "4xlarge"
					args.WorkerSizeIsSet = true
					args.WorkerType = "m5"
					args.WorkerTypeIsSet = true

					configAfterLoad = configInBucket
					configAfterLoad.AllowIPs = "\"88.98.225.40/32\""
					configAfterLoad.AutoCert = false
					configAfterLoad.ConcourseWebSize = args.WebSize
					configAfterLoad.ConcourseWorkerCount = args.WorkerCount
					configAfterLoad.ConcourseWorkerSize = args.WorkerSize
					configAfterLoad.Domain = args.Domain
					configAfterLoad.GithubClientID = args.GithubAuthClientID
					configAfterLoad.GithubClientSecret = args.GithubAuthClientSecret
					configAfterLoad.HostedZoneID = "ABC123"
					configAfterLoad.HostedZoneRecordPrefix = "ci."
					configAfterLoad.PrivateCIDR = "10.0.1.0/24"
					configAfterLoad.PublicCIDR = "10.0.0.0/24"
					configAfterLoad.RDSInstanceClass = "db.t2.4xlarge"
					configAfterLoad.SourceAccessIP = "192.0.2.0"
					configAfterLoad.Tags = args.Tags
					configAfterLoad.WorkerType = args.WorkerType
					configAfterLoad.VMProvisioningType = config.ON_DEMAND

					terraformInputVars = &terraform.GCPInputVars{
						AllowIPs:           configAfterLoad.AllowIPs,
						ConfigBucket:       configAfterLoad.ConfigBucket,
						DBName:             configAfterLoad.RDSDefaultDatabaseName,
						DBPassword:         configAfterLoad.RDSPassword,
						DBTier:             configAfterLoad.RDSInstanceClass,
						DBUsername:         configAfterLoad.RDSUsername,
						Deployment:         configAfterLoad.Deployment,
						DNSManagedZoneName: configAfterLoad.HostedZoneID,
						DNSRecordSetPrefix: configAfterLoad.HostedZoneRecordPrefix,
						ExternalIP:         configAfterLoad.SourceAccessIP,
						GCPCredentialsJSON: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
						Namespace:          configAfterLoad.Namespace,
						PrivateCIDR:        configAfterLoad.PrivateCIDR,
						Project:            "gcp-project",
						PublicCIDR:         configAfterLoad.PublicCIDR,
						Region:             configAfterLoad.Region,
						Tags:               "",
						Zone:               "europe-west1-b",
					}

					configAfterCreateEnv = configAfterLoad
					configAfterCreateEnv.ConcourseCert = args.TLSCert
					configAfterCreateEnv.ConcourseKey = args.TLSKey
					configAfterCreateEnv.DirectorPublicIP = "99.99.99.99"
					configAfterCreateEnv.Tags = append([]string{"control-tower-version=some version"}, args.Tags...)
					configAfterCreateEnv.Version = "some version"

					configAfterConcourseDeploy = configAfterCreateEnv
					configAfterConcourseDeploy.CredhubURL = "https://ci.google.com:8844/"
					configAfterConcourseDeploy.ConcourseCACert = ""
					configAfterConcourseDeploy.ConcourseCert = "i-am-a-tls-cert"
					configAfterConcourseDeploy.ConcourseKey = "i-am-a-tls-key"
					configAfterConcourseDeploy.DirectorCACert = `-----BEGIN CERTIFICATE-----
MIIEXTCCAsWgAwIBAgIQZiZWMIod+NGTx+jJ8mBIbzANBgkqhkiG9w0BAQsFADA4
MQwwCgYDVQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMM
B2Jvc2hfY2EwHhcNMTkwMjEzMTAyNTM1WhcNMjAwMjEzMTAyNTM1WjAmMQwwCgYD
VQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkwggGiMA0GCSqGSIb3DQEB
AQUAA4IBjwAwggGKAoIBgQDCSub74gKqpTNFLeeEHNAH9a4Cf1ITQ11iK6OmzCM0
NloX6/o2ER23AHAXBIJLPEVX4qnQKNeQjKFcicSdTK0kVVuLa5mlFY4/ieCmXKA+
jmeXPJQGdzFi8BgoLAITcnFGGZY8kwPCzhhrPfa7TcvJnK/2RtKOwgWMxK2kxqs+
EtA2fxZb57EV05kS7ctoHfiSjAOKqWlsMGOon0z22HItuvV8hcEB85oyv9AbN6Ni
GoaktghNEz3A9T0d2iJBMX7uZgEKmq1VwhqTUAXbr+kxN23Dc1m2b7eMHZ0GyOes
Puwj6ZG9Zqypf+wyb5ndZWwxFAex6Ery02W0rBFKne9J4VRxOzy/IgJKc1bvqtjs
EpU1FbDCw6tLb9PKltEO7AQMrx70ubYuVt4exWfZVhzHNBzhII7gmLegHB0eGWou
KLWV2hDM9OgDdbfmSubqTN+7szTvlUZTAwsLiTUQMCT7JpJSjqs7jXOO59PT0qSn
W/QT8Q/BwiQrCeNAjzVhHU8CAwEAAaN1MHMwDgYDVR0PAQH/BAQDAgWgMBMGA1Ud
JQQMMAoGCCsGAQUFBwMBMAwGA1UdEwEB/wQCMAAwHQYDVR0OBBYEFMaEYqmheOXo
GU3n6SiMLazbnxh5MB8GA1UdIwQYMBaAFHii4fiqAwJSnNhi6C+ibr/4OOTyMA0G
CSqGSIb3DQEBCwUAA4IBgQCmjAuwHHby7HlvBgGGkBwqEtwX6r4rgh0XNXJVyh6J
cCvGpKzDBl+XX8a1KQ4T1f5L81TR4JeV94DUMQ9xvOD2foBa6BMjDD4rQB216HlF
sTh05eHp3TnLD7+Usu3iLQX2kYViEXdFh33xs3SD63PoKl2xS8h4PRenouaDH8Lz
QT2zsqZMAy7zTwLa6A746OVwUT2xngkpTpFFsyIrNbwtvYuF54mqCQ7lw1Rfx1KE
eXZl7o48YRh/IOuDZPjdyQqgOQeOhBqH8MLd3iWTyat/0jwd/VhIKpwxRZqlhlI+
gEjWiNnil1JTl/I1AetpHX2oACkhg67IUR0MsbGSL2/KruVlHuLZdQMSNq0wBh6H
Ni/w26gJvipll4mPV/Kr/LyWXQy2tkqhF00/fdhoZpWh93xzbaffriy+eaPqvFCc
9HeOuco3Br39wORSRNL5Gb5ARgb30Z8syPEPSkt/g93Kj7wKiiiIFp+psuiIgnxh
EWtqtr5TdtFYrxertqRY2vI=
-----END CERTIFICATE-----
`
				})

				JustBeforeEach(func() {
					configClient.LoadReturns(configInBucket, nil)
					configClient.ConfigExistsReturns(true, nil)
					configClient.HasAssetReturnsOnCall(0, true, nil)
					configClient.LoadAssetReturnsOnCall(0, directorStateFixture, nil)
					configClient.HasAssetReturnsOnCall(1, true, nil)
					configClient.LoadAssetReturnsOnCall(1, directorCredsFixture, nil)
				})

				It("updates config and calls collaborators with the current arguments", func() {
					client := buildClient()
					err := client.Deploy()
					Expect(err).ToNot(HaveOccurred())

					Expect(configClient).To(HaveReceived("ConfigExists"))
					Expect(configClient).To(HaveReceived("Load"))
					Expect(tfInputVarsFactory).To(HaveReceived("NewInputVars").With(configAfterLoad))

					Expect(terraformCLI).To(HaveReceived("Apply").With(terraformInputVars))
					Expect(terraformCLI).To(HaveReceived("BuildOutput").With(terraformInputVars))
					Expect(configClient).To(HaveReceived("Update").With(configAfterLoad))

					Expect(configClient).To(HaveReceived("HasAsset").With("director-state.json"))
					Expect(configClient.HasAssetArgsForCall(0)).To(Equal("director-state.json"))
					Expect(configClient).To(HaveReceived("LoadAsset").With("director-state.json"))
					Expect(configClient.LoadAssetArgsForCall(0)).To(Equal("director-state.json"))
					Expect(configClient).To(HaveReceived("HasAsset").With("director-creds.yml"))
					Expect(configClient.HasAssetArgsForCall(1)).To(Equal("director-creds.yml"))
					Expect(configClient).To(HaveReceived("LoadAsset").With("director-creds.yml"))
					Expect(configClient.LoadAssetArgsForCall(1)).To(Equal("director-creds.yml"))
					Expect(boshClient).To(HaveReceived("Deploy").With(directorStateFixture, directorCredsFixture, false))

					Expect(configClient).To(HaveReceived("StoreAsset").With("director-state.json", directorStateFixture))
					Expect(configClient).To(HaveReceived("StoreAsset").With("director-creds.yml", directorCredsFixture))
					Expect(boshClient).To(HaveReceived("Cleanup"))
					Expect(flyClient).To(HaveReceived("SetDefaultPipeline").With(configAfterCreateEnv, false))
					Expect(configClient).To(HaveReceived("Update").With(configAfterConcourseDeploy))
				})
			})
		})

		Context("a new deployment with no CLI args", func() {
			var defaultGeneratedConfig, configAfterLoad, configAfterCreateEnv, configAfterConcourseDeploy config.Config
			BeforeEach(func() {
				// Config generated by default for a new deployment
				defaultGeneratedConfig = config.Config{
					AllowIPs:                 "\"0.0.0.0/0\"",
					AvailabilityZone:         "europe-west1-b",
					ConcoursePassword:        "",
					ConcourseUsername:        "",
					ConcourseWebSize:         "small",
					ConcourseWorkerCount:     1,
					ConcourseWorkerSize:      "xlarge",
					ConfigBucket:             "control-tower-initial-deployment-europe-west1-config",
					DirectorHMUserPassword:   "generatedPassword20",
					DirectorMbusPassword:     "generatedPassword20",
					DirectorNATSPassword:     "generatedPassword20",
					Deployment:               "control-tower-initial-deployment",
					DirectorPassword:         "generatedPassword20",
					DirectorRegistryPassword: "generatedPassword20",
					DirectorUsername:         "admin",
					EncryptionKey:            "generatedPassword32",
					IAAS:                     "GCP",
					PrivateCIDR:              "10.0.1.0/24",
					PrivateKey:               "private",
					Project:                  "sample_project",
					PublicCIDR:               "10.0.0.0/24",
					PublicKey:                "public",
					RDSDefaultDatabaseName:   "bosh-8letters",
					RDSInstanceClass:         "db.t2.small",
					RDSPassword:              "generatedPassword20",
					RDSUsername:              "admingeneratedPassword7",
					Region:                   "europe-west1",
					SourceAccessIP:           "192.0.2.0",
					TFStatePath:              "terraform.tfstate",
					WorkerType:               "m4",
					VMProvisioningType:       config.SPOT,
				}

				//Mutations we expect to have been done after load
				configAfterLoad = defaultGeneratedConfig
				configAfterLoad.AllowIPs = "\"0.0.0.0/0\""
				configAfterLoad.AutoCert = false
				configAfterLoad.SourceAccessIP = "192.0.2.0"

				//Mutations we expect to have been done after deploying the director
				configAfterCreateEnv = configAfterLoad
				configAfterCreateEnv.DirectorPublicIP = "99.99.99.99"
				configAfterCreateEnv.Domain = "77.77.77.77"
				configAfterCreateEnv.Tags = []string{"control-tower-version=some version"}
				configAfterCreateEnv.Version = "some version"

				// Mutations we expect to have been done after deploying Concourse
				configAfterConcourseDeploy = configAfterCreateEnv
				configAfterConcourseDeploy.ConcourseUsername = "admin"
				configAfterConcourseDeploy.CredhubAdminClientSecret = "hxfgb56zny2yys6m9wjx"
				configAfterConcourseDeploy.CredhubCACert = `-----BEGIN CERTIFICATE-----
MIIEXTCCAsWgAwIBAgIQSmhcetyHDHLOYGaqMnJ0QTANBgkqhkiG9w0BAQsFADA4
MQwwCgYDVQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMM
B2Jvc2hfY2EwHhcNMTkwMjEzMTAyNTM0WhcNMjAwMjEzMTAyNTM0WjA4MQwwCgYD
VQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMMB2Jvc2hf
Y2EwggGiMA0GCSqGSIb3DQEBAQUAA4IBjwAwggGKAoIBgQC+0bA9T4awlJYSn6aq
un6Hylu47b2UiZpFZpvPomKWPay86QaJ0vC9SK8keoYI4gWwsZSAMXp2mSCkXKRi
+rVc+sKnzv9VgPoVY5eYIYCtJvl7KCJQE02dGoxuGOaWlBiHuD6TzY6lI9fNxkAW
eMGR3UylJ7ET0NvgAZWS1daov2GfiKkaYUCdbY8DtfhMyFhJ381VNHwoP6xlZbSf
TInO/2TS8xpW2BcMNhFAu9MJVtC5pDHtJtkXHXep027CkrPjtFQWpzvIMvPAtZ68
9t46nS9Ix+RmeN3v+sawNzbZscnsslhB+m4GrpL9M8g8sbweMw9yxf241z1qkiNJ
to3HRqqyNyGsvI9n7OUrZ4D5oAfY7ze1TF+nxnkmJp14y21FEdG7t76N0J5dn6bJ
/lroojig/PqabRsyHbmj6g8N832PEQvwsPptihEwgrRmY6fcBbMUaPCpNuVTJVa5
g0KdBGDYDKTMlEn4xaj8P1wRbVjtXVMED2l4K4tS/UiDIb8CAwEAAaNjMGEwDgYD
VR0PAQH/BAQDAgEGMA8GA1UdEwEB/wQFMAMBAf8wHQYDVR0OBBYEFHii4fiqAwJS
nNhi6C+ibr/4OOTyMB8GA1UdIwQYMBaAFHii4fiqAwJSnNhi6C+ibr/4OOTyMA0G
CSqGSIb3DQEBCwUAA4IBgQAGXDTlsQWIJHfvU3zy9te35adKOUeDwk1lSe4NYvgW
FJC0w2K/1ZldmQ2leHmiXSukDJAYmROy9Y1qkUazTzjsdvHGhUF2N1p7fIweNj8e
csR+T21MjPEwD99m5+xLvnMRMuqzH9TqVbFIM3lmCDajh8n9cp4KvGkQmB+X7DE1
R6AXG4EN9xn91TFrqmFFNOrFtoAjtag05q/HoqMhFFVeg+JTpsPshFjlWIkzwqKx
pn68KG2ztgS0KeDraGKwItTKengTCr/VkgorXnhKcI1C6C5iRXZp3wREu8RO+wRe
KSGbsYIHaFxd3XwW4JnsW+hes/W5MZX01wkwOLrktf85FjssBZBavxBbyFag/LvS
8oULOZRLYUkuElM+0Wzf8ayB574Fd97gzCVzWoD0Ei982jAdbEfk77PV1TvMNmEn
3M6ktB7GkjuD9OL12iNzxmbQe7p1WkYYps9hK4r0pbyxZPZlPMmNNZo579rywDjF
wEW5QkylaPEkbVDhJWeR1I8=
-----END CERTIFICATE-----
`
				configAfterConcourseDeploy.CredhubPassword = "f4b12bc0166cad1bc02b050e4e79ac4c"
				configAfterConcourseDeploy.CredhubURL = "https://77.77.77.77:8844/"
				configAfterConcourseDeploy.CredhubUsername = "credhub-cli"
				configAfterConcourseDeploy.ConcourseCACert = `-----BEGIN CERTIFICATE-----
MIIEXTCCAsWgAwIBAgIQZiZWMIod+NGTx+jJ8mBIbzANBgkqhkiG9w0BAQsFADA4
MQwwCgYDVQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMM
B2Jvc2hfY2EwHhcNMTkwMjEzMTAyNTM1WhcNMjAwMjEzMTAyNTM1WjAmMQwwCgYD
VQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkwggGiMA0GCSqGSIb3DQEB
AQUAA4IBjwAwggGKAoIBgQDCSub74gKqpTNFLeeEHNAH9a4Cf1ITQ11iK6OmzCM0
NloX6/o2ER23AHAXBIJLPEVX4qnQKNeQjKFcicSdTK0kVVuLa5mlFY4/ieCmXKA+
jmeXPJQGdzFi8BgoLAITcnFGGZY8kwPCzhhrPfa7TcvJnK/2RtKOwgWMxK2kxqs+
EtA2fxZb57EV05kS7ctoHfiSjAOKqWlsMGOon0z22HItuvV8hcEB85oyv9AbN6Ni
GoaktghNEz3A9T0d2iJBMX7uZgEKmq1VwhqTUAXbr+kxN23Dc1m2b7eMHZ0GyOes
Puwj6ZG9Zqypf+wyb5ndZWwxFAex6Ery02W0rBFKne9J4VRxOzy/IgJKc1bvqtjs
EpU1FbDCw6tLb9PKltEO7AQMrx70ubYuVt4exWfZVhzHNBzhII7gmLegHB0eGWou
KLWV2hDM9OgDdbfmSubqTN+7szTvlUZTAwsLiTUQMCT7JpJSjqs7jXOO59PT0qSn
W/QT8Q/BwiQrCeNAjzVhHU8CAwEAAaN1MHMwDgYDVR0PAQH/BAQDAgWgMBMGA1Ud
JQQMMAoGCCsGAQUFBwMBMAwGA1UdEwEB/wQCMAAwHQYDVR0OBBYEFMaEYqmheOXo
GU3n6SiMLazbnxh5MB8GA1UdIwQYMBaAFHii4fiqAwJSnNhi6C+ibr/4OOTyMA0G
CSqGSIb3DQEBCwUAA4IBgQCmjAuwHHby7HlvBgGGkBwqEtwX6r4rgh0XNXJVyh6J
cCvGpKzDBl+XX8a1KQ4T1f5L81TR4JeV94DUMQ9xvOD2foBa6BMjDD4rQB216HlF
sTh05eHp3TnLD7+Usu3iLQX2kYViEXdFh33xs3SD63PoKl2xS8h4PRenouaDH8Lz
QT2zsqZMAy7zTwLa6A746OVwUT2xngkpTpFFsyIrNbwtvYuF54mqCQ7lw1Rfx1KE
eXZl7o48YRh/IOuDZPjdyQqgOQeOhBqH8MLd3iWTyat/0jwd/VhIKpwxRZqlhlI+
gEjWiNnil1JTl/I1AetpHX2oACkhg67IUR0MsbGSL2/KruVlHuLZdQMSNq0wBh6H
Ni/w26gJvipll4mPV/Kr/LyWXQy2tkqhF00/fdhoZpWh93xzbaffriy+eaPqvFCc
9HeOuco3Br39wORSRNL5Gb5ARgb30Z8syPEPSkt/g93Kj7wKiiiIFp+psuiIgnxh
EWtqtr5TdtFYrxertqRY2vI=
-----END CERTIFICATE-----
`
				configAfterConcourseDeploy.ConcourseCert = "existing Cert"
				configAfterConcourseDeploy.ConcourseKey = "existing Key"
				configAfterConcourseDeploy.DirectorCACert = `-----BEGIN CERTIFICATE-----
MIIEXTCCAsWgAwIBAgIQZiZWMIod+NGTx+jJ8mBIbzANBgkqhkiG9w0BAQsFADA4
MQwwCgYDVQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkxEDAOBgNVBAMM
B2Jvc2hfY2EwHhcNMTkwMjEzMTAyNTM1WhcNMjAwMjEzMTAyNTM1WjAmMQwwCgYD
VQQGEwNVU0ExFjAUBgNVBAoTDUNsb3VkIEZvdW5kcnkwggGiMA0GCSqGSIb3DQEB
AQUAA4IBjwAwggGKAoIBgQDCSub74gKqpTNFLeeEHNAH9a4Cf1ITQ11iK6OmzCM0
NloX6/o2ER23AHAXBIJLPEVX4qnQKNeQjKFcicSdTK0kVVuLa5mlFY4/ieCmXKA+
jmeXPJQGdzFi8BgoLAITcnFGGZY8kwPCzhhrPfa7TcvJnK/2RtKOwgWMxK2kxqs+
EtA2fxZb57EV05kS7ctoHfiSjAOKqWlsMGOon0z22HItuvV8hcEB85oyv9AbN6Ni
GoaktghNEz3A9T0d2iJBMX7uZgEKmq1VwhqTUAXbr+kxN23Dc1m2b7eMHZ0GyOes
Puwj6ZG9Zqypf+wyb5ndZWwxFAex6Ery02W0rBFKne9J4VRxOzy/IgJKc1bvqtjs
EpU1FbDCw6tLb9PKltEO7AQMrx70ubYuVt4exWfZVhzHNBzhII7gmLegHB0eGWou
KLWV2hDM9OgDdbfmSubqTN+7szTvlUZTAwsLiTUQMCT7JpJSjqs7jXOO59PT0qSn
W/QT8Q/BwiQrCeNAjzVhHU8CAwEAAaN1MHMwDgYDVR0PAQH/BAQDAgWgMBMGA1Ud
JQQMMAoGCCsGAQUFBwMBMAwGA1UdEwEB/wQCMAAwHQYDVR0OBBYEFMaEYqmheOXo
GU3n6SiMLazbnxh5MB8GA1UdIwQYMBaAFHii4fiqAwJSnNhi6C+ibr/4OOTyMA0G
CSqGSIb3DQEBCwUAA4IBgQCmjAuwHHby7HlvBgGGkBwqEtwX6r4rgh0XNXJVyh6J
cCvGpKzDBl+XX8a1KQ4T1f5L81TR4JeV94DUMQ9xvOD2foBa6BMjDD4rQB216HlF
sTh05eHp3TnLD7+Usu3iLQX2kYViEXdFh33xs3SD63PoKl2xS8h4PRenouaDH8Lz
QT2zsqZMAy7zTwLa6A746OVwUT2xngkpTpFFsyIrNbwtvYuF54mqCQ7lw1Rfx1KE
eXZl7o48YRh/IOuDZPjdyQqgOQeOhBqH8MLd3iWTyat/0jwd/VhIKpwxRZqlhlI+
gEjWiNnil1JTl/I1AetpHX2oACkhg67IUR0MsbGSL2/KruVlHuLZdQMSNq0wBh6H
Ni/w26gJvipll4mPV/Kr/LyWXQy2tkqhF00/fdhoZpWh93xzbaffriy+eaPqvFCc
9HeOuco3Br39wORSRNL5Gb5ARgb30Z8syPEPSkt/g93Kj7wKiiiIFp+psuiIgnxh
EWtqtr5TdtFYrxertqRY2vI=
-----END CERTIFICATE-----
`
			})

			JustBeforeEach(func() {
				configClient.NewConfigReturns(config.Config{
					ConfigBucket: "control-tower-initial-deployment-europe-west1-config",
					Deployment:   "control-tower-initial-deployment",
					Namespace:    "",
					Project:      "sample_project",
					Region:       "europe-west1",
					TFStatePath:  "terraform.tfstate",
				})
				configClient.HasAssetReturnsOnCall(0, false, nil)
				configClient.HasAssetReturnsOnCall(1, false, nil)
			})

			It("does the right things in the right order", func() {
				client := buildClient()
				err := client.Deploy()
				Expect(err).ToNot(HaveOccurred())

				terraformInputVars := &terraform.GCPInputVars{
					AllowIPs:           defaultGeneratedConfig.AllowIPs,
					ConfigBucket:       defaultGeneratedConfig.ConfigBucket,
					DBName:             defaultGeneratedConfig.RDSDefaultDatabaseName,
					DBPassword:         defaultGeneratedConfig.RDSPassword,
					DBTier:             defaultGeneratedConfig.RDSInstanceClass,
					DBUsername:         defaultGeneratedConfig.RDSUsername,
					Deployment:         defaultGeneratedConfig.Deployment,
					DNSManagedZoneName: defaultGeneratedConfig.HostedZoneID,
					DNSRecordSetPrefix: defaultGeneratedConfig.HostedZoneRecordPrefix,
					ExternalIP:         defaultGeneratedConfig.SourceAccessIP,
					GCPCredentialsJSON: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
					Namespace:          defaultGeneratedConfig.Namespace,
					PrivateCIDR:        defaultGeneratedConfig.PrivateCIDR,
					Project:            "gcp-project",
					PublicCIDR:         defaultGeneratedConfig.PublicCIDR,
					Region:             defaultGeneratedConfig.Region,
					Tags:               "",
					Zone:               "europe-west1-b",
				}

				tfInputVarsFactory.NewInputVarsReturns(terraformInputVars)

				Expect(configClient).To(HaveReceived("EnsureBucketExists"))
				Expect(configClient).To(HaveReceived("ConfigExists"))
				Expect(configClient).ToNot(HaveReceived("Load"))
				Expect(tfInputVarsFactory).To(HaveReceived("NewInputVars").With(defaultGeneratedConfig))
				Expect(configClient).To(HaveReceived("Update").With(defaultGeneratedConfig))
				Expect(terraformCLI).To(HaveReceived("Apply").With(terraformInputVars))
				Expect(terraformCLI).To(HaveReceived("BuildOutput").With(terraformInputVars))
				Expect(configClient).To(HaveReceived("Update").With(configAfterLoad))
				Expect(configClient).To(HaveReceived("HasAsset").With("director-state.json"))
				Expect(configClient.HasAssetArgsForCall(0)).To(Equal("director-state.json"))
				Expect(configClient).To(HaveReceived("HasAsset").With("director-creds.yml"))
				Expect(configClient.HasAssetArgsForCall(1)).To(Equal("director-creds.yml"))
				Expect(boshClient).To(HaveReceived("Deploy").With([]byte{}, []byte{}, false))
				Expect(configClient).To(HaveReceived("StoreAsset").With("director-state.json", directorStateFixture))
				Expect(configClient).To(HaveReceived("StoreAsset").With("director-creds.yml", directorCredsFixture))
				Expect(boshClient).To(HaveReceived("Cleanup"))
				Expect(flyClient).To(HaveReceived("SetDefaultPipeline").With(configAfterCreateEnv, false))
				Expect(configClient).To(HaveReceived("Update").With(configAfterConcourseDeploy))
			})
		})

		It("Prints a warning about changing the sourceIP", func() {
			client := buildClient()
			err := client.Deploy()
			Expect(err).ToNot(HaveOccurred())

			Expect(stderr).To(gbytes.Say("WARNING: allowing access from local machine"))
		})

		Context("When a custom domain was previously configured", func() {
			BeforeEach(func() {
				configInBucket.Domain = "ci.google.com"
			})

			JustBeforeEach(func() {
				configClient.LoadReturns(configInBucket, nil)
				configClient.ConfigExistsReturns(true, nil)
			})

			It("Prints a warning about adding a DNS record", func() {
				client := buildClient()
				err := client.Deploy()
				Expect(err).ToNot(HaveOccurred())

				Expect(stderr).To(gbytes.Say("WARNING: adding record ci.google.com to DNS zone google.com with name ABC123"))
			})

			Context("and a custom cert is provided", func() {
				BeforeEach(func() {
					args.TLSCert = "--- CERTIFICATE ---"
					args.TLSCertIsSet = true
					args.TLSKey = "--- KEY ---"
					args.TLSKeyIsSet = true
				})

				It("Prints the correct domain and not suggest using --insecure", func() {
					client := buildClient()
					err := client.Deploy()
					Expect(err).ToNot(HaveOccurred())
					Eventually(stdout).Should(gbytes.Say("DEPLOY SUCCESSFUL"))
					Eventually(stdout).Should(gbytes.Say("fly --target happymeal login --concourse-url https://ci.google.com --username admin --password s3cret"))
				})
			})
		})

		Context("When the user tries to change the region of an existing deployment", func() {
			BeforeEach(func() {
				args.Region = "europe-west2"
			})

			JustBeforeEach(func() {
				configClient.LoadReturns(configInBucket, nil)
				configClient.ConfigExistsReturns(true, nil)
			})
			It("Returns a meaningful error message", func() {
				client := buildClientOtherRegion()
				err := client.Deploy()
				Expect(err).To(MatchError("found previous deployment in europe-west1. Refusing to deploy to europe-west2 as changing regions for existing deployments is not supported"))
			})
		})

		Context("When the user tries to change the availability zone of an existing deployment", func() {
			BeforeEach(func() {
				args.Zone = "europe-west1c"
				args.ZoneIsSet = true
			})

			JustBeforeEach(func() {
				configClient.LoadReturns(configInBucket, nil)
				configClient.ConfigExistsReturns(true, nil)
			})
			It("Returns a meaningful error message", func() {
				client := buildClient()
				err := client.Deploy()
				Expect(err).To(MatchError("error getting initial config before deploy: [Existing deployment uses zone europe-west1a and cannot change to zone europe-west1c]"))
			})
		})

		Context("When a custom DB instance size is not provided", func() {
			BeforeEach(func() {
				args.DBSize = "small"
				args.DBSizeIsSet = false
			})

			JustBeforeEach(func() {
				configClient.LoadReturns(configInBucket, nil)
				configClient.ConfigExistsReturns(true, nil)
			})
			It("Does not override the existing DB size", func() {
				provider, err := iaas.New(iaas.GCP, "europe-west1")
				Expect(err).ToNot(HaveOccurred())
				gcpInputVarsFactory, err := concourse.NewTFInputVarsFactory(provider)
				Expect(err).ToNot(HaveOccurred())

				var passedDBSize string
				tfInputVarsFactory.NewInputVarsStub = func(config config.ConfigView) terraform.InputVars {
					passedDBSize = config.GetRDSInstanceClass()
					return gcpInputVarsFactory.NewInputVars(config)
				}

				client := buildClient()
				err = client.Deploy()
				Expect(err).ToNot(HaveOccurred())

				Expect(passedDBSize).To(Equal(configInBucket.RDSInstanceClass))
			})
		})

		Context("When running in self-update mode and the concourse is already deployed", func() {
			It("Sets the default pipeline, before deploying the bosh director", func() {
				flyClient.CanConnectStub = func() (bool, error) {
					return true, nil
				}
				args.SelfUpdate = true

				client := buildClient()
				err := client.Deploy()
				Expect(err).ToNot(HaveOccurred())

				Expect(boshClient).To(HaveReceived("Deploy").With([]byte{}, []byte{}, true))
			})
		})
	})
})