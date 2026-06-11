# Polar merchant-of-record setup (owner runbook)

Everything in the codebase is wired and waiting on four values from a Polar
account. Only the account creation itself needs a human with identity and
banking details. ~30 minutes.

## 1. Create the account (only step code can't do)

1. <https://polar.sh/signup> → sign up (GitHub login works).
2. Create organization: **goforge**.
3. Finish payout onboarding (Stripe Connect identity + bank). Sales work
   before payout verification completes; money holds until it does.

## 2. Create the product

1. Products → New Product.
2. Name **Anvil Pro**, description: "Unlimited scheduling links and a
   footer-free booking page for one anvil deployment. Priority support."
3. Pricing: **Subscription**, **$90/year**.

## 3. Attach the License Keys benefit

1. On the product → Benefits → add **License Keys**.
2. Prefix: `ANVIL_`.
3. Activation limit: **5**.
4. Expiry: tied to subscription (key dies when the sub lapses).

## 4. Wire the four values

| Value | Where to find it | Where it goes |
|---|---|---|
| Organization ID | Polar org settings | GitHub repo variable `POLAR_ORG_ID` (release workflow bakes it via ldflags) |
| Checkout URL | Product → Share → checkout link | GitHub repo variable `BUY_URL` **and** `buyUrl` in `dev.goforge/content/anvil.md` front matter |
| — | — | After setting both repo variables: re-tag or `gh workflow run` so released binaries carry them |
| Webhooks | none needed | validation is pull-based from the binary |

```sh
gh variable set POLAR_ORG_ID --repo brain-fuel/anvil --body "<org id>"
gh variable set BUY_URL --repo brain-fuel/anvil --body "<checkout url>"
```

## 5. Verify end to end (sandbox first)

Polar has a sandbox (<https://sandbox.polar.sh>, API
`https://sandbox-api.polar.sh`). Repeat steps 2–3 there, make a test
purchase with Stripe test card `4242 4242 4242 4242`, then:

```sh
ANVIL_LICENSE_API=https://sandbox-api.polar.sh/v1/customer-portal \
ANVIL_LICENSE_ORG=<sandbox org id> \
  anvil license activate ANVIL_TEST_KEY
ANVIL_LICENSE_API=… ANVIL_LICENSE_ORG=… anvil license status
```

Expect `licensed: true, status: granted`. Then run `anvil serve` with a
2-link config — it must boot. Deactivate/revoke the key in the Polar
dashboard, rerun `status`, expect degradation.

## 6. Go live checklist

- [ ] Production product + license benefit created
- [ ] `POLAR_ORG_ID` + `BUY_URL` repo variables set
- [ ] `buyUrl` front-matter param updated on goforge.dev/anvil + site deployed
- [ ] New tag pushed → release binaries carry the org id
- [ ] One real $90 self-purchase, activate, refund — proves the whole loop
