package e2e

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strconv"

	api "github.com/appscode/kubed/apis/kubed/v1alpha1"
	"github.com/appscode/kubed/test/framework"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apps "k8s.io/api/apps/v1beta1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Snapshotter", func() {
	var (
		f             *framework.Invocation
		cred          core.Secret
		stopCh        chan struct{}
		clusterConfig api.ClusterConfig
		backend       *api.Backend
		deployment    *apps.Deployment
	)

	BeforeEach(func() {
		f = root.Invoke()
	})

	AfterEach(func() {
		close(stopCh)
	})

	JustBeforeEach(func() {
		var err error
		if missing, _ := BeZero().Match(cred); missing && backend.Local == nil {
			Skip("Missing backend credential")
		}

		if backend.Local == nil {
			err := f.CreateSecret(cred)
			Expect(err).NotTo(HaveOccurred())
		}

		err = f.CreateBucketIfNotExist(clusterConfig.Snapshotter.Backend)
		Expect(err).NotTo(HaveOccurred())

		By("Starting Operator")
		stopCh = make(chan struct{})
		err = f.RunOperator(stopCh, clusterConfig)
		Expect(err).NotTo(HaveOccurred())
	})

	shouldTakeClusterSnapshot := func() {
		f.EventuallyBackupSnapshot(*backend).ShouldNot(BeEmpty())
	}

	Describe("Take Snapshot of Cluster in", func() {
		Context(`"Minio" backend`, func() {
			AfterEach(func() {
				f.DeleteMinioServer()
			})

			BeforeEach(func() {
				minikubeIP := net.IP{192, 168, 99, 100}

				By("Creating Minio server with cacert")
				_, err := f.CreateMinioServer(true, []net.IP{minikubeIP})
				Expect(err).NotTo(HaveOccurred())

				msvc, err := f.KubeClient.CoreV1().Services(f.Namespace()).Get("minio-service", metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				minioServiceNodePort := strconv.Itoa(int(msvc.Spec.Ports[0].NodePort))
				minioEndpoint := fmt.Sprintf("https://" + minikubeIP.String() + ":" + minioServiceNodePort)

				cred = f.SecretForMinioBackend(true)

				backend = framework.NewMinioBackend("kubed-test", "demo", minioEndpoint, cred.Name)
				clusterConfig = framework.SnapshotterClusterConfig(backend)
			})

			It(`should backup cluster Snapshot`, shouldTakeClusterSnapshot)
		})

		Context(`"Local" backend`, func() {
			AfterEach(func() {
				os.RemoveAll(framework.TEST_LOCAL_BACKUP_DIR)
			})

			BeforeEach(func() {
				err := os.MkdirAll(framework.TEST_LOCAL_BACKUP_DIR, 0777)
				Expect(err).NotTo(HaveOccurred())

				backend = framework.NewLocalBackend(framework.TEST_LOCAL_BACKUP_DIR)
				clusterConfig = framework.SnapshotterClusterConfig(backend)
			})

			It(`should backup cluster Snapshot`, shouldTakeClusterSnapshot)
		})
	})

	Describe("Sanitize backed up object", func() {
		Context(`"Local" backend`, func() {
			AfterEach(func() {
				os.RemoveAll(framework.TEST_LOCAL_BACKUP_DIR)
				f.DeleteDeployment(deployment.ObjectMeta)
			})

			BeforeEach(func() {
				err := os.MkdirAll(framework.TEST_LOCAL_BACKUP_DIR, 0777)
				Expect(err).NotTo(HaveOccurred())

				backend = framework.NewLocalBackend(framework.TEST_LOCAL_BACKUP_DIR)
				clusterConfig = framework.SnapshotterClusterConfig(backend)

				deployment = f.Deployment()
				_, err = f.CreateDeployment(*deployment)
				Expect(err).NotTo(HaveOccurred())
				f.WaitUntilDeploymentReady(deployment.ObjectMeta)
			})

			It(`should sanitize backed up deployment`, func() {
				shouldTakeClusterSnapshot()

				By("Listing backed up snapshots")
				files, err := ioutil.ReadDir(framework.TEST_LOCAL_BACKUP_DIR)
				Expect(err).NotTo(HaveOccurred())
				Expect(files).NotTo(BeEmpty())

				By("Exrtacting snapshot tarball")
				file, err := os.Open(filepath.Join(framework.TEST_LOCAL_BACKUP_DIR, files[0].Name()))
				Expect(err).NotTo(HaveOccurred())
				defer file.Close()
				err = framework.Untar(framework.TEST_LOCAL_BACKUP_DIR, file)
				Expect(err).NotTo(HaveOccurred())

				By("Reading deployment's yaml from backed up snapshot")
				dpl, err := framework.ReadYaml(deployment.Name + ".yaml")
				Expect(err).NotTo(HaveOccurred())

				By("Checking deployment snapshot is sanitized")
				err = framework.DeploymentSnapshotSanitized(dpl)
				Expect(err).NotTo(HaveOccurred())

			})
		})
	})

})
