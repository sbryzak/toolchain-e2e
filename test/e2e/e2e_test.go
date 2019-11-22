package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	authsupport "github.com/codeready-toolchain/toolchain-common/pkg/test/auth"
	"github.com/codeready-toolchain/toolchain-e2e/testsupport"
	"github.com/codeready-toolchain/toolchain-e2e/wait"

	userv1 "github.com/openshift/api/user/v1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	uuid "github.com/satori/go.uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestE2EFlow(t *testing.T) {
	// given
	// full flow from usersignup with approval down to namespaces creation
	ctx, awaitility := testsupport.WaitForDeployments(t, &toolchainv1alpha1.UserSignupList{})
	defer ctx.Cleanup()

	// Create multiple accounts and let them get provisioned while we are executing the main flow for "johnsmith" and "extrajohn"
	// We will verify them in the end of the test
	usernames := createMultipeSignups(t, ctx, awaitility, 5)

	// Create and approve "johnsmith" and "extrajohn" signups
	johnsmithName := "johnsmith"
	johnSignup, expUaSpec := setup(t, ctx, awaitility, johnsmithName)
	extrajohnName := "extrajohn"
	_, expExtraUaSpec := setup(t, ctx, awaitility, extrajohnName)

	// Expected ns template revisions
	revisions, err := getRevisions(awaitility)
	require.NoError(t, err)

	verifyResources(t, awaitility, johnsmithName, revisions,
		wait.UntilMasterUserRecordHasConditions(provisioned()),
		wait.UntilUserAccountHasSpec(*expUaSpec),
		wait.UntilUserAccountHasConditions(provisioned()))
	verifyResources(t, awaitility, extrajohnName, revisions,
		wait.UntilMasterUserRecordHasConditions(provisioned()),
		wait.UntilUserAccountHasSpec(*expExtraUaSpec),
		wait.UntilUserAccountHasConditions(provisioned()))

	t.Run("try to break UserAccount", func(t *testing.T) {

		t.Run("delete user and wait until is recreated", func(t *testing.T) {
			// given
			user := &userv1.User{}
			err := awaitility.Client.Get(context.TODO(), types.NamespacedName{Name: johnsmithName}, user)
			require.NoError(t, err)

			// when
			err = awaitility.Client.Delete(context.TODO(), user)

			// then
			require.NoError(t, err)
			verifyResources(t, awaitility, johnsmithName, revisions,
				wait.UntilMasterUserRecordHasConditions(provisioned()),
				wait.UntilUserAccountHasConditions(provisioned()))
			verifyResources(t, awaitility, extrajohnName, revisions,
				wait.UntilMasterUserRecordHasConditions(provisioned()),
				wait.UntilUserAccountHasConditions(provisioned()))
		})

		t.Run("delete identity and wait until is recreated", func(t *testing.T) {
			// given
			identity := &userv1.Identity{}
			err := awaitility.Client.Get(context.TODO(), types.NamespacedName{Name: toIdentityName(expUaSpec.UserID)}, identity)
			require.NoError(t, err)

			// when
			err = awaitility.Client.Delete(context.TODO(), identity)

			// then
			require.NoError(t, err)
			verifyResources(t, awaitility, johnsmithName, revisions,
				wait.UntilMasterUserRecordHasConditions(provisioned()),
				wait.UntilUserAccountHasConditions(provisioned()))
			verifyResources(t, awaitility, extrajohnName, revisions,
				wait.UntilMasterUserRecordHasConditions(provisioned()),
				wait.UntilUserAccountHasConditions(provisioned()))
		})

		t.Run("delete user mapping and wait until is recreated", func(t *testing.T) {
			// given
			user := &userv1.User{}
			err := awaitility.Client.Get(context.TODO(), types.NamespacedName{Name: johnsmithName}, user)
			require.NoError(t, err)

			// when
			user.Identities = []string{}
			err = awaitility.Client.Update(context.TODO(), user)

			// then
			require.NoError(t, err)
			verifyResources(t, awaitility, johnsmithName, revisions,
				wait.UntilMasterUserRecordHasConditions(provisioned()),
				wait.UntilUserAccountHasConditions(provisioned()))
			verifyResources(t, awaitility, extrajohnName, revisions,
				wait.UntilMasterUserRecordHasConditions(provisioned()),
				wait.UntilUserAccountHasConditions(provisioned()))
		})

		t.Run("delete identity mapping and wait until is recreated", func(t *testing.T) {
			// given
			identity := &userv1.Identity{}
			err := awaitility.Client.Get(context.TODO(), types.NamespacedName{Name: toIdentityName(expUaSpec.UserID)}, identity)
			require.NoError(t, err)
			identity.User = corev1.ObjectReference{Name: "", UID: ""}

			// when
			err = awaitility.Client.Update(context.TODO(), identity)

			// then
			require.NoError(t, err)
			verifyResources(t, awaitility, johnsmithName, revisions,
				wait.UntilMasterUserRecordHasConditions(provisioned()),
				wait.UntilUserAccountHasConditions(provisioned()))
			verifyResources(t, awaitility, extrajohnName, revisions,
				wait.UntilMasterUserRecordHasConditions(provisioned()),
				wait.UntilUserAccountHasConditions(provisioned()))
		})
	})

	t.Run("delete UserSignup and expect all resources to be deleted", func(t *testing.T) {
		// given
		hostAwait := wait.NewHostAwaitility(awaitility)
		memberAwait := wait.NewMemberAwaitility(awaitility)
		johnSignup, err := hostAwait.WaitForUserSignup(johnSignup.Name)
		require.NoError(t, err)

		// when
		err = awaitility.Client.Delete(context.TODO(), johnSignup)

		// then
		require.NoError(t, err)
		t.Logf("MasterUserRecord '%s' deleted", johnsmithName)

		err = hostAwait.WaitUntilMasterUserRecordDeleted(johnsmithName)
		assert.NoError(t, err, "MasterUserRecord is not deleted")

		err = memberAwait.WaitUntilUserAccountDeleted(johnsmithName)
		assert.NoError(t, err, "UserAccount is not deleted")

		err = memberAwait.WaitUntilUserDeleted(johnsmithName)
		assert.NoError(t, err, "User is not deleted")

		err = memberAwait.WaitUntilIdentityDeleted(johnsmithName)
		assert.NoError(t, err, "Identity is not deleted")

		err = memberAwait.WaitUntilNSTemplateSetDeleted(johnsmithName)
		assert.NoError(t, err, "NSTemplateSet id not deleted")

		err = memberAwait.WaitUntilNamespaceDeleted(johnsmithName, "code")
		assert.NoError(t, err, "johnsmith-code namnespace is not deleted")

		err = memberAwait.WaitUntilNamespaceDeleted(johnsmithName, "dev")
		assert.NoError(t, err, "johnsmith-dev namnespace is not deleted")

		err = memberAwait.WaitUntilNamespaceDeleted(johnsmithName, "stage")
		assert.NoError(t, err, "johnsmith-stage namnespace is not deleted")

		// also, verify that other user's resource are left intact
		_, err = hostAwait.WaitForMasterUserRecord(extrajohnName)
		require.NoError(t, err)
		verifyResources(t, awaitility, extrajohnName, revisions, wait.UntilMasterUserRecordHasConditions(provisioned()))
	})

	t.Run("multiple MasterUserRecord resources provisioned", func(t *testing.T) {
		// Now when the main flow has been tested we can verify the signups we created in the very beginning
		verifyMultipeSignups(t, awaitility, usernames, revisions)
	})
}

func setup(t *testing.T, ctx *framework.TestCtx, awaitility *wait.Awaitility, username string) (*toolchainv1alpha1.UserSignup, *toolchainv1alpha1.UserAccountSpec) {
	// 0. Verify that the `basic` NSTemplateTier resource exists (will be needed later)
	revisions, err := getRevisions(awaitility)
	require.NoError(t, err)

	// 1. Create a UserSignup resource
	identity := &authsupport.Identity{
		ID:       uuid.NewV4(),
		Username: username,
	}
	registration(t, awaitility.RegistrationServiceURL, *identity)

	// at this stage, the usersignup should not be approved nor completed
	userSignup, err := awaitility.Host().WaitForUserSignup(identity.ID.String(), wait.UntilUserSignupHasConditions(pendingApproval()...))
	require.NoError(t, err)

	// 2. approve the UserSignup
	userSignup.Spec.Approved = true
	err = awaitility.Host().Client.Update(context.TODO(), userSignup)
	require.NoError(t, err)
	// Check the updated conditions
	_, err = awaitility.Host().WaitForUserSignup(userSignup.Name, wait.UntilUserSignupHasConditions(approvedByAdmin()...))
	require.NoError(t, err)

	return userSignup, &v1alpha1.UserAccountSpec{
		UserID:   userSignup.Name,
		Disabled: false,
		NSLimit:  "default",
		NSTemplateSet: toolchainv1alpha1.NSTemplateSetSpec{
			TierName: "basic",
			Namespaces: []toolchainv1alpha1.NSTemplateSetNamespace{
				{
					Type:     "code",
					Revision: revisions["code"],
					Template: "", // must be empty
				},
				{
					Type:     "dev",
					Revision: revisions["dev"],
					Template: "", // must be empty
				},
				{
					Type:     "stage",
					Revision: revisions["stage"],
					Template: "", // must be empty
				},
			},
		},
	}
}

func createMultipeSignups(t *testing.T, ctx *framework.TestCtx, awaitility *wait.Awaitility, capacity int) []string {
	usernames := make([]string, capacity)
	for i := 0; i < capacity; i++ {
		// Create an approved UserSignup resource
		userSignup := newUserSignup(t, awaitility.Host(), fmt.Sprintf("multiple-signup-testuser-%d", i))
		userSignup.Spec.Approved = true
		err := awaitility.Host().Client.Create(context.TODO(), userSignup, testsupport.CleanupOptions(ctx))
		awaitility.T.Logf("created UserSignup with username: '%s' and resource name: '%s'", userSignup.Spec.Username, userSignup.Name)
		require.NoError(t, err)
		usernames[i] = userSignup.Spec.Username
	}
	return usernames
}

func verifyMultipeSignups(t *testing.T, awaitility *wait.Awaitility, usernames []string, revisions map[string]string) {
	for _, username := range usernames {
		verifyResources(t, awaitility, username, revisions,
			wait.UntilMasterUserRecordHasConditions(provisioned()),
			wait.UntilUserAccountHasConditions(provisioned()))
	}
}

func getRevisions(awaitility *wait.Awaitility) (map[string]string, error) {
	basicTier, err := awaitility.Host().WaitForNSTemplateTier("basic", wait.UntilNSTemplateTierSpec(wait.Not(wait.HasNamespaceRevisions("000000a"))))
	if err != nil {
		return nil, err
	}
	revisions := make(map[string]string, 3)
	for _, typ := range []string{"code", "dev", "stage"} {
		if r, found := namespaceRevision(*basicTier, typ); found {
			revisions[typ] = r
			continue
		}
		return nil, fmt.Errorf("unable to find revision for '%s' namespace in the 'basic' NSTemplateTier", typ)
	}
	return revisions, nil
}

func namespaceRevision(tier v1alpha1.NSTemplateTier, typ string) (string, bool) {
	for _, ns := range tier.Spec.Namespaces {
		if ns.Type == typ {
			return ns.Revision, true
		}
	}
	return "", false
}

func verifyResources(t *testing.T, awaitility *wait.Awaitility, murName string, expectedRevisions map[string]string, mixedCriteria ...interface{}) {
	masteruserrecordCriteria := []wait.MasterUserRecordWaitCriterion{}
	useraccountCriteria := []wait.UserAccountWaitCriterion{}
	for _, c := range mixedCriteria {
		switch c := c.(type) {
		case wait.MasterUserRecordWaitCriterion:
			masteruserrecordCriteria = append(masteruserrecordCriteria, c)
		case wait.UserAccountWaitCriterion:
			useraccountCriteria = append(useraccountCriteria, c)
		default:
			t.Fatalf("unknown type of criterion: %T", c)
		}
	}

	hostAwait := wait.NewHostAwaitility(awaitility)

	// first, wait for the MasterUserRecord to exist, no matter its status
	mur, err := hostAwait.WaitForMasterUserRecord(murName)
	require.NoError(t, err)

	// then wait for the associated UserAccount to exist, with the given criteria
	memberAwait := wait.NewMemberAwaitility(awaitility)
	userAccount, err := memberAwait.WaitForUserAccount(mur.Name, append(useraccountCriteria, wait.UntilUserAccountHasSpec(mur.Spec.UserAccounts[0].Spec))...)
	require.NoError(t, err)
	require.NotNil(t, userAccount)

	// and finally, check again the MasterUserRecord with the expected (embedded) UserAccount status, on top of the other criteria
	uaStatus := toolchainv1alpha1.UserAccountStatusEmbedded{
		TargetCluster:     mur.Spec.UserAccounts[0].TargetCluster,
		UserAccountStatus: userAccount.Status,
	}
	_, err = hostAwait.WaitForMasterUserRecord(mur.Name, append(masteruserrecordCriteria, wait.UntilMasterUserRecordHasUserAccountStatuses(uaStatus))...)
	assert.NoError(t, err)

	_, err = memberAwait.WaitForUser(userAccount.Name)
	assert.NoError(t, err)

	_, err = memberAwait.WaitForIdentity(toIdentityName(userAccount.Spec.UserID))
	assert.NoError(t, err)

	_, err = memberAwait.WaitForNSTmplSet(userAccount.Name)
	assert.NoError(t, err)

	// Verify all namespaces and RoleBindings in these namespaces
	assert.Len(t, expectedRevisions, 3)
	for key, revision := range expectedRevisions {
		ns, err := memberAwait.WaitForNamespace(userAccount.Name, key, revision)
		require.NoError(t, err)
		rb, err := memberAwait.WaitForRoleBinding(ns, "user-edit")
		require.NoError(t, err)
		assert.Len(t, rb.Subjects, 1)
		assert.Equal(t, "User", rb.Subjects[0].Kind)
		assert.Equal(t, userAccount.Name, rb.Subjects[0].Name)
		assert.Equal(t, "edit", rb.RoleRef.Name)
	}
}

func toIdentityName(userID string) string {
	return fmt.Sprintf("%s:%s", "rhd", userID)
}

func registration(t *testing.T, route string, identity authsupport.Identity) {
	// Call signup endpoint with an valid token.
	emailClaim := authsupport.WithEmailClaim(uuid.NewV4().String() + "@email.tld")
	iatClaim := authsupport.WithIATClaim(time.Now().Add(-60 * time.Second))
	token, err := authsupport.GenerateSignedE2ETestToken(identity, emailClaim, iatClaim)
	require.NoError(t, err)

	req, err := http.NewRequest("POST", route+"/api/v1/signup", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("content-type", "application/json")
	client := getClient()

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
}