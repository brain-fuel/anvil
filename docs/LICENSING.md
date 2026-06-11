# Licensing

## Tiers

| | Free | Anvil Pro — $90/year per deployment |
|---|---|---|
| Library, `find`, `agenda` CLI | ✓ | ✓ |
| `anvil serve` scheduling links | 1 link | unlimited |
| Calendar sources (iCal/CalDAV/Google) | all | all |
| Agenda PWA | ✓ | ✓ |
| Booking-page footer | "Powered by anvil" | removed |
| Support | issues | priority email |

The free tier is not a trial. One scheduling link, every feature on it,
forever.

## How activation works

Purchases go through [Polar](https://polar.sh) (merchant of record — they
handle VAT/sales tax worldwide). You receive a license key `ANVIL_…`.

```sh
anvil license activate ANVIL_XXXX        # registers this deployment
anvil license status                     # current standing
```

- Activation and daily revalidation call Polar's public license endpoints;
  no account or secret is stored on your server beyond the key itself.
- State lives in `~/.config/anvil/license.json` (or
  `$XDG_CONFIG_HOME/anvil/`).
- **Offline grace**: if the validation service is unreachable, a previously
  valid license stays good for **14 days**. A running server never crashes
  over licensing — worst case it degrades to the free tier and logs why.
- Keys allow 5 activations (home server + VPS + laptop is fine). Refunds
  and cancellation follow Polar's standard policy; a cancelled subscription
  expires the key at period end.

## The MIT question, answered plainly

Anvil's source is MIT. Yes, you can read the gate, and yes, you could
delete it. The license is how a one-person project stays maintained — the
people who pay are buying ongoing calendar-provider whack-a-mole, security
fixes, and the roadmap, not a decryption key. If you're a company using
anvil to schedule real meetings, buy the license. If you're a hobbyist with
two links and no budget, the free tier plus a fork is morally fine and we
both know it.
