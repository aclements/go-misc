# Generate Google Sheets token

Go to https://console.developers.google.com/.

- Create a new GCP project, or use an existing one. I called mine `proposal-minutes`.
- Go to IAM & Admin > Service Acccounts.
- Click "+ Create Service Account".
- Enter a service account name (I used `proposal-minutes`).
- Enter a description, like `proposal minutes bot`
- Click "Create and Continue"
- Skip the "Grant this service account access to project". Click "Continue"
- Skip the "Grant users access to this service account". Click "Continue".
- Back at the Service Accounts screen, click on the email address for the new service account,
  bringing up the "Service account details" page.
- Click the "Keys" tab.
- Click "Add Key", then "Create New Key", then "JSON", then "Continue".
- Copy the downloaded file to to `~/.config/proposal-minutes/gdoc-service.json`
  (use `~/Library/Application Support/proposal-minutes/gdoc-service.json` on a Mac).
- Go back to the Details tab and copy the email address for the account, something like `proposal-minutes@proposal-minutes.iam.gserviceaccount.com`.

In the Proposal Minutes v3 spreadsheet, click Share and then add that email address as an editor of the doc.

# Generate GitHub token

Go to GitHub, then Account Settings > Developer Options > Personal Access
Tokens > Fine-grained Tokens.

Click "Generate new token"

The name of the token can be anything. I used `proposal-minutes`. The expiration
can be set up to one year out.

Set the "Resource owner" to "golang", then select "Only select repositories" and
add the "golang/go" repository.

Under "Repository permissions", set "Issues" to "Read and write".

Under "Organization permissions", set "Projects" to "Read and write".

Save the token.

Copy the access token and save it to `~/.config/proposal-minutes/github.tok`.
