package apiGatewayDeploy

import (
	"encoding/json"
	"net/url"

	"net/http/httptest"

	"net/http"

	"github.com/30x/apid-core"
	"github.com/apigee-labs/transicator/common"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("listener", func() {

	Context("ApigeeSync snapshot event", func() {

		/*
		 * Note that the test snapshot should not be empty.
		 * If it's empty, you can't use deploymentsResult chan to mark the end of processing,
		 * so the threads generated by startupOnExistingDatabase() will mess up later tests
		 */
		It("should set DB to appropriate version", func(done Done) {
			deploymentID := "set_version_test"
			snapshot, dep := createSnapshotDeployment(deploymentID)

			db, err := data.DBVersion(snapshot.SnapshotInfo)
			Expect(err).ShouldNot(HaveOccurred())

			err = InitDB(db)
			Expect(err).ShouldNot(HaveOccurred())

			insertDeploymentToDb(dep, db)
			expectedDB, err := data.DBVersion(snapshot.SnapshotInfo)
			Expect(err).NotTo(HaveOccurred())

			var listener = make(chan deploymentsResult)
			addSubscriber <- listener

			apid.Events().Emit(APIGEE_SYNC_EVENT, &snapshot)

			result := <-listener
			Expect(result.err).ShouldNot(HaveOccurred())

			// DB should have been set
			Expect(getDB() == expectedDB).Should(BeTrue())
			close(done)
		})

		It("should process unready on existing db startup event", func(done Done) {

			deploymentID := "startup_test"

			snapshot, dep := createSnapshotDeployment(deploymentID)

			db, err := data.DBVersion(snapshot.SnapshotInfo)
			Expect(err).ShouldNot(HaveOccurred())

			err = InitDB(db)
			Expect(err).ShouldNot(HaveOccurred())

			insertDeploymentToDb(dep, db)

			var listener = make(chan deploymentsResult)
			addSubscriber <- listener

			apid.Events().Emit(APIGEE_SYNC_EVENT, &snapshot)

			result := <-listener
			Expect(result.err).ShouldNot(HaveOccurred())

			Expect(len(result.deployments)).To(Equal(1))
			d := result.deployments[0]

			Expect(d.ID).To(Equal(deploymentID))
			close(done)
		})

		It("should send deployment statuses on existing db startup event", func(done Done) {

			successDep := DataDeployment{
				ID:                 "success",
				LocalBundleURI:     "x",
				DeployStatus:       RESPONSE_STATUS_SUCCESS,
				DeployErrorCode:    1,
				DeployErrorMessage: "message",
			}

			failDep := DataDeployment{
				ID:                 "fail",
				LocalBundleURI:     "x",
				DeployStatus:       RESPONSE_STATUS_FAIL,
				DeployErrorCode:    1,
				DeployErrorMessage: "message",
			}

			blankDep := DataDeployment{
				ID:             "blank",
				LocalBundleURI: "x",
			}

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()

				var results apiDeploymentResults
				err := json.NewDecoder(r.Body).Decode(&results)
				Expect(err).ToNot(HaveOccurred())

				Expect(results).To(HaveLen(2))

				Expect(results).To(ContainElement(apiDeploymentResult{
					ID:        successDep.ID,
					Status:    successDep.DeployStatus,
					ErrorCode: successDep.DeployErrorCode,
					Message:   successDep.DeployErrorMessage,
				}))
				Expect(results).To(ContainElement(apiDeploymentResult{
					ID:        failDep.ID,
					Status:    failDep.DeployStatus,
					ErrorCode: failDep.DeployErrorCode,
					Message:   failDep.DeployErrorMessage,
				}))

				close(done)
			}))

			var err error
			apiServerBaseURI, err = url.Parse(ts.URL)
			Expect(err).NotTo(HaveOccurred())

			// init without info == startup on existing DB
			var snapshot = common.Snapshot{
				SnapshotInfo: "test",
				Tables:       []common.Table{},
			}

			db, err := data.DBVersion(snapshot.SnapshotInfo)
			Expect(err).NotTo(HaveOccurred())

			err = InitDB(db)
			Expect(err).NotTo(HaveOccurred())

			tx, err := db.Begin()
			Expect(err).ShouldNot(HaveOccurred())

			err = InsertDeployment(tx, successDep)
			Expect(err).ShouldNot(HaveOccurred())
			err = InsertDeployment(tx, failDep)
			Expect(err).ShouldNot(HaveOccurred())
			err = InsertDeployment(tx, blankDep)
			Expect(err).ShouldNot(HaveOccurred())

			err = tx.Commit()
			Expect(err).ShouldNot(HaveOccurred())

			apid.Events().Emit(APIGEE_SYNC_EVENT, &snapshot)
		})
	})

	Context("ApigeeSync change event", func() {

		It("inserting event should deliver the deployment to subscribers", func(done Done) {

			deploymentID := "add_test_1"

			event, dep := createChangeDeployment(deploymentID)

			insertDeploymentToDb(dep, getDB())

			var listener = make(chan deploymentsResult)
			addSubscriber <- listener

			apid.Events().Emit(APIGEE_SYNC_EVENT, &event)

			// wait for event to propagate
			result := <-listener
			Expect(result.err).ShouldNot(HaveOccurred())

			deployments, err := getReadyDeployments()
			Expect(err).ShouldNot(HaveOccurred())

			Expect(len(deployments)).To(Equal(1))
			d := deployments[0]

			Expect(d.ID).To(Equal(deploymentID))
			Expect(d.BundleName).To(Equal(dep.BundleName))
			Expect(d.BundleURI).To(Equal(dep.BundleURI))

			close(done)
		})

		It("delete event should deliver to subscribers", func(done Done) {

			deploymentID := "delete_test_1"

			// insert deployment
			event, dep := createChangeDeployment(deploymentID)
			insertDeploymentToDb(dep, getDB())
			listener := make(chan deploymentsResult)
			addSubscriber <- listener
			apid.Events().Emit(APIGEE_SYNC_EVENT, &event)
			// wait for event to propagate
			result := <-listener
			Expect(result.err).ShouldNot(HaveOccurred())

			// delete deployment
			deletDeploymentFromDb(dep, getDB())
			row := common.Row{}
			row["id"] = &common.ColumnVal{Value: deploymentID}
			event = common.ChangeList{
				Changes: []common.Change{
					{
						Operation: common.Delete,
						Table:     DEPLOYMENT_TABLE,
						OldRow:    row,
					},
				},
			}

			listener = make(chan deploymentsResult)
			addSubscriber <- listener
			apid.Events().Emit(APIGEE_SYNC_EVENT, &event)
			result = <-listener
			Expect(result.err).ShouldNot(HaveOccurred())
			Expect(len(result.deployments)).To(Equal(0))
			close(done)
		})
	})
})

func createChangeDeployment(deploymentID string) (common.ChangeList, DataDeployment) {
	uri, err := url.Parse(testServer.URL)
	Expect(err).ShouldNot(HaveOccurred())

	uri.Path = "/bundles/1"
	bundleUri := uri.String()
	bundle := bundleConfigJson{
		Name:         uri.Path,
		URI:          bundleUri,
		ChecksumType: "crc32",
	}
	bundle.Checksum = testGetChecksum(bundle.ChecksumType, bundleUri)
	bundle1Json, err := json.Marshal(bundle)
	Expect(err).ShouldNot(HaveOccurred())

	row := common.Row{}
	row["id"] = &common.ColumnVal{Value: deploymentID}
	row["bundle_config_json"] = &common.ColumnVal{Value: string(bundle1Json)}

	changeList := common.ChangeList{
		Changes: []common.Change{
			{
				Operation: common.Insert,
				Table:     DEPLOYMENT_TABLE,
				NewRow:    row,
			},
		},
	}
	dep, err := dataDeploymentFromRow(changeList.Changes[0].NewRow)
	return changeList, dep
}

func insertDeploymentToDb(dep DataDeployment, db apid.DB) {
	tx, err := db.Begin()
	Expect(err).ShouldNot(HaveOccurred())
	defer tx.Rollback()
	err = InsertDeployment(tx, dep)
	Expect(err).ShouldNot(HaveOccurred())
	err = tx.Commit()
	Expect(err).ShouldNot(HaveOccurred())
}

func deletDeploymentFromDb(dep DataDeployment, db apid.DB) {
	tx, err := db.Begin()
	Expect(err).ShouldNot(HaveOccurred())
	defer tx.Rollback()
	err = deleteDeployment(tx, dep.ID)
	Expect(err).ShouldNot(HaveOccurred())
	err = tx.Commit()
	Expect(err).ShouldNot(HaveOccurred())
}

func createSnapshotDeployment(deploymentID string) (common.Snapshot, DataDeployment) {
	uri, err := url.Parse(testServer.URL)
	Expect(err).ShouldNot(HaveOccurred())

	uri.Path = "/bundles/1"
	bundleUri := uri.String()
	bundle := bundleConfigJson{
		Name:         uri.Path,
		URI:          bundleUri,
		ChecksumType: "crc32",
	}
	bundle.Checksum = testGetChecksum(bundle.ChecksumType, bundleUri)

	dep := DataDeployment{
		ID:                 deploymentID,
		DataScopeID:        deploymentID,
		BundleURI:          bundle.URI,
		BundleChecksum:     bundle.Checksum,
		BundleChecksumType: bundle.ChecksumType,
	}

	// init without info == startup on existing DB
	var snapshot = common.Snapshot{
		SnapshotInfo: "test",
		Tables:       []common.Table{},
	}
	return snapshot, dep
}
