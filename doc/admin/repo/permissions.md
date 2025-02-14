# Repository permissions

Sourcegraph can be configured to enforce repository permissions from code hosts.

Currently, GitHub, GitHub Enterprise, GitLab and Bitbucket Server permissions are supported. Check the [roadmap](../../dev/roadmap.md) for plans to
support other code hosts. If your desired code host is not yet on the roadmap, please [open a
feature request](https://github.com/sourcegraph/sourcegraph/issues/new?template=feature_request.md).

## GitHub

Prerequisite: [Add GitHub as an authentication provider.](../auth.md#github)

Then, [add or edit a GitHub external service](../external_service/github.md#repository-syncing) and include the `authorization` field:

```json
{
   "url": "https://github.com",
   "token": "$PERSONAL_ACCESS_TOKEN",
   "authorization": {
     "ttl": "3h"
   }
}
```

## GitLab

GitLab permissions can be configured in three ways:

1. Set up GitLab as an OAuth sign-on provider for Sourcegraph (recommended)
2. Use a GitLab sudo-level personal access token in conjunction with another SSO provider
   (recommended only if the first option is not possible)
3. Assume username equivalency between Sourcegraph and GitLab (warning: this is generally unsafe and
   should only be used if you are using strictly `http-header` authentication).

### OAuth application

Prerequisite: [Add GitLab as an authentication provider.](../auth.md#gitlab)

Then, [add or edit a GitLab external service](../external_service/gitlab.md#repository-syncing) and include the `authorization` field:

```json
{
  "url": "https://gitlab.com",
  "token": "$PERSONAL_ACCESS_TOKEN",
  "authorization": {
    "identityProvider": {
      "type": "oauth"
    },
    "ttl": "3h"
  }
}
```

### Sudo access token

Prerequisite: Add the [SAML](../auth.md#saml) or [OpenID Connect](../auth.md#openid-connect)
authentication provider you use to sign into GitLab.

Then, [add or edit a GitLab external service](../external_service/gitlab.md#repository-syncing) and include the `authorization` field:

```json
{
  "url": "https://gitlab.com",
  "token": "$PERSONAL_ACCESS_TOKEN",
  "authorization": {
    "identityProvider": {
      "type": "external",
      "authProviderID": "$AUTH_PROVIDER_ID",
      "authProviderType": "$AUTH_PROVIDER_TYPE",
      "gitlabProvider": "$AUTH_PROVIDER_GITLAB_ID"
    },
    "ttl": "3h"
  }
}
```

`$AUTH_PROVIDER_ID` and `$AUTH_PROVIDER_TYPE` identify the authentication provider to use and should
match the fields specified in the authentication provider config
(`auth.providers`). `$AUTH_PROVIDER_GITLAB_ID` should match the `identities.provider` returned by
[the admin GitLab Users API endpoint](https://docs.gitlab.com/ee/api/users.html#for-admins).

### Username

Prerequisite: Ensure that `http-header` is the *only* authentication provider type configured for
Sourcegraph. If this is not the case, then it will be possible for users to escalate privileges,
because Sourcegraph usernames are mutable.

[Add or edit a GitLab external service](../external_service/gitlab.md#repository-syncing) and include the `authorization` field:

```json
{
  "url": "https://gitlab.com",
  "token": "$PERSONAL_ACCESS_TOKEN",
  "authorization": {
    "identityProvider": {
      "type": "username"
    },
    "ttl": "3h"
  }
}
```

## Bitbucket Server

Enforcing Bitbucket Server permissions can be configured via the `authorization` setting in its external service configuration.

### Prerequisites

1. You have **fewer than 2500 repositories** on your Bitbucket Server instance.
1. You have the exact same user accounts, **with matching usernames**, in Sourcegraph and Bitbucket Server. This can be accomplished by configuring an [external authentication provider](../auth.md) that mirrors user accounts from a central directory like LDAP or Active Directory. The same should be done on Bitbucket Server with [external user directories](https://confluence.atlassian.com/bitbucketserver/external-user-directories-776640394.html).
1. Ensure you have set `auth.enableUsernameChanges` to **`false`** in the [Critical site config](../config/critical_config.md) to prevent users from changing their usernames and **escalating their privileges**.


### Setup

This section walks you through the process of setting up an *Application Link between Sourcegraph and Bitbucket Server* and configuring the Bitbucket Server external service with `authorization` settings. It assumes the above prerequisites are met.

As an admin user, go to the "Application Links" page. You can use the sidebar navigation in the admin dashboard, or go directly to [https://bitbucketserver.example.com/plugins/servlet/applinks/listApplicationLinks](https://bitbucketserver.example.com/plugins/servlet/applinks/listApplicationLinks).

<img src="https://imgur.com/Hg4bzOf.png" width="800">

---

Write Sourcegraph's external URL in the text area (e.g. `https://sourcegraph.example.com`) and click **Create new link**. Click **Continue** even if Bitbucket Server warns you about the given URL not responding.

<img src="https://imgur.com/x6vFKIL.png" width="800">

---

Write `Sourcegraph` as the *Application Name* and select `Generic Application` as the *Application Type*. Leave everything else unset and click **Continue**.

<img src="https://imgur.com/161rbB9.png" width="800">

---


Now click the edit button in the `Sourcegraph` Application Link that you just created and select the `Incoming Authentication` panel.

<img src="https://imgur.com/sMGmzhH.png" width="800">

---


Generate a *Consumer Key* in your terminal with `echo sourcegraph$(openssl rand -hex 16)`. Copy this command's output and paste it in the *Consumer Key* field. Write `Sourcegraph` in the *Consumer Name* field.

<img src="https://imgur.com/1kK2Y5x.png" width="800">

---

Generate an RSA key pair in your terminal with `openssl genrsa -out sourcegraph.pem 4096 && openssl rsa -in sourcegraph.pem -pubout > sourcegraph.pub`. Copy the contents of `sourcegraph.pub` and paste them in the *Public Key* field.

<img src="https://imgur.com/YHm1uSr.png" width="800">

---

Scroll to the bottom and check the *Allow 2-Legged OAuth* checkbox, then write your admin account's username in the *Execute as* field and, lastly, check the *Allow user impersonation through 2-Legged OAuth* checkbox. Press **Save**.

<img src="https://imgur.com/1qxEAye.png" width="800">

---

Go to your Sourcegraph's external services page (i.e. `https://sourcegraph.example.com/site-admin/external-services`) and either edit or create a new *Bitbucket Server* external service. Click on the *Enforce permissions* quick action on top of the configuration editor. Copy the *Consumer Key* you generated before to the `oauth.consumerKey` field and the output of the command `base64 sourcegraph.pem | tr -d '\n'` to the `oauth.signingKey` field. Finally, **save the configuration**.

<img src="https://imgur.com/ucetesA.png" width="800">

---

You're done! :tada:
