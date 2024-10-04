# Generate Google Sheets token

Go to https://console.developers.google.com/.

Create a new GCP project. I called mine `proposal-minutes`.

Configure the OAuth consent screen: Go to APIs & Services > OAuth consent
screen. Select "Internal" and click "Create". Enter an app name. I called it
`proposal-minutes`. Fill in other required fields, though most can be left
blank. Click "Save and continue". You don't need to add any scopes. Click "Save
and continue".

Enable Google Sheets: Go to APIs & Services > Enabled APIs and Services. Click
"Enable APIs and Services". Search for the "Google Sheets API" and enable it.

Create OAuth credentials: Go to APIs & Services > Credentials. Click Create
Credentials > OAuth client ID. Select "Desktop app", give it a name (I used
`proposal-minutes`, again), and click Create. On the next screen, click
"Download JSON" and save this file as `~/.config/proposal-minutes/gdoc.json`.

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
