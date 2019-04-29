## faxme
Receives faxes from [Twilio](https://www.twilio.com) and sends notification(s) with link to received fax document.

## overview
The purpose of `faxme` is to provide a way to receive faxes without a fax machine or extra phone line at home. In a nutshell, this is how it works:
1. Fax is sent to your new Twilio fax number (more on this below)
2. Twilio makes a web request to `faxme`, notifying it that someone wants to send you a fax
3. `faxme` verifies that the request is legit (based on config) and either accepts or rejects the fax
4. If accepted, Twilio makes a second web request to `faxme` once the fax has been fully recieved
5. `faxme` uses Twilio to send text/SMS messages to all configured phone numbers with a link to the fax document

 NOTE: there are recurring costs involved. At the time this README was written, the cost is less than typical online fax services that cost ~$10/month. E.g., at this time:
 * [DigitalOcean](https://www.digitalocean.com) droplet: $5/month (could possibly be replaced using [ngrok](https://ngrok.com/pricing) or similar)
 * Twilio fax number: $1/month
 * Twilio per minute: pennies for a 2 - 3 page fax

`faxme` can be even more cost effective if used to handle multiple fax numbers.

## install
```
go get github.com/dgnorton/faxme
```

## `faxme` setup
`faxme` needs to run on an Internet-accessable server so that Twilio can send it notifications. The recommended setup is with `faxme` using HTTPS/TLS and with a `username` & `password` configured. DigitalOcean has instructions for creating a droplet and using [letsencrypt](https://letsencrypt.org) for certificats. Try [Googling DigitalOcean letsencrypt](https://www.google.com/search?&q=digitalocean+letsencrypt&ie=utf-8&oe=utf-8).

If setting up a DigitalOcean droplet, you'll also need to add a firewall and create a rule to forward the `faxme` port to the droplet.

Once you've selected a server, copy the `faxme` binary to it. Run `faxme -h` to get a list of command line arguments. Most of the command line arguments can alternatively be read from a TOML file or environment variables.

NOTES:
* Running `faxme` without TLS enabled or without username and password set is considered unsafe and will require starting `faxme` with the `-unsafe` command line argument.
* Currently, the `-skip-req-val` argument must be provided because the request validation feature is incomplete.
* Twilio SID and Token should be kept private. It's probably best to store them in a config file or environment variables.
* Providing `-fax` and `-sms` arguments is the simplest way to map a single fax number to a single mobile number that will receive fax notifications. To support multiple fax numbers and/or multiple notifications per fax, specify an `accounts.json` file. The format of that file is covered below.

Supported TOML:

```
http-bind-address = "127.0.0.1"
http-port = "7500"
http-user = "twilio"
http-pwd = "LongAndVerySecurePassword"
tls-cert-file = "/etc/letsencrypt/live/ursheeting.me/fullchain.pem"
tls-key-file = "/etc/letsencrypt/live/ursheeting.me/privkey.pen"
fax-number = "12223334444"
mobile-number = "17778889999"
accounts-file = "/path/to/accounts.json"
twilio-sid = "ACXXXXXXXXXXXXXXXXXXXXXXXXX"
twilio-token = "XXXXXXXXXXXXXXXXXXXXXXXXXXX"
```

Environment variables:

Same as the TOML settings but add `FAXME_` prefix, uppercase everything, and use `_` instead of `-`. For example:
```
FAXME_HTTP_BIND_ADDRESS="127.0.0.1"
FAXME_HTTP_PORT="7500"
FAXME_HTTP_USER="twilio"
```

Example `accounts.json` file:
```
[
  {
    "fax_number": "12223334444",
    "contacts": ["14443332222", "15556667777"]
  },
  {
    "fax_number:" "18889990000",
    "contacts": ["14445556666"]
  }
]
```

## Twilio setup (generic instructions, as Twilio may change its UI)
1. Create or login to your Twilio account
2. Buy a new phone number that supports both SMS and fax
3. Go to manage numbers and select the new number
4. Set `Accept Incoming` to `Faxes`
5. Set `A fax comes in` to `webhook` and enter the URL to your `faxme` service. Examples:
   * `https://username:password@mydomain.com:7500/fax/recieve?to=12223334444` (recommended)
   * `https://mydomain.com:7500/fax/receive?to=12223334444` (no auth - not recommended)
   * `http://mydomain.com:7500/fax/recieve?to=12223334444` (no encryption or auth - NOT recommended)
   * NOTE: replace `12223334444` with your new fax number
   * NOTE: the `username` and `password` above should be new and different from your Twilio login. Needs to match `faxme`'s `http-user` and `http-pwd` configuration settings in previous section.
   
## testing and debug
Send yourself a fax using the following command:
```
curl -X POST https://fax.twilio.com/v1/Faxes \
--data-urlencode "From=+12223334444" \
--data-urlencode "To=+1222333444" \
--data-urlencode "MediaUrl=https://www.twilio.com/docs/documents/25/justthefaxmaam.pdf" \
-u $FAXME_TWILIO_SID:$FAXME_TWILIO_TOKEN
```
After some seconds, `faxme` should log a request to the `/fax/receive` endpoint. If the request is successful, `faxme` will return and log a short clip of XML: `<Response><Receive action="/fax/received?to=12223334444"/></Response>`. That response instructs Twilio that it's okay to receive the fax (as opposed to rejecting it). Several minutes later, `faxme` should log a request to the `/fax/received` endpoint and a short time later you should receive a text message with a link to the document recieved.

If `faxme` logs nothing, make sure the port `faxme` is listening on is not blocked by a firewall. You can also check the debug log on your Twilio account dashboard. There may be useful debug info there.
