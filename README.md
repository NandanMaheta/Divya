# Swastik

Swastik is an internal app that automates the everyday work of applying for IPOs in the Indian stock market.

With 20+ IPOs launching in the Indian market, keeping track of all of them by hand gets messy. The person running things has to decide which IPOs to apply for, tell everyone, share the UPI ID to apply with, remind people before the deadline, check who got shares, work out the profit share, and keep track of who has paid. This app does all of that in one place.

## What it does

- Marks every IPO as **Apply** or **Do not apply**, so clients know straight away what to do.
- Gives each client a simple "apply" task for every family account, with the UPI ID to use and reminders until it's done.
- Records who got shares and the listing price, and works out each person's profit share automatically.
- Shows each client what they owe or will receive, and keeps track of payments.
- Has reports by month, quarter or year, for the admin and for each client.

It has a web admin panel for the person running it and a mobile app for clients (Android, plus a web version for iPhone). It's private and invite-only.

## Tech used

- **Backend:** Go, PostgreSQL
- **Admin panel:** React (Vite, TanStack, Tailwind)
- **Mobile app:** React Native with Expo
- **Data helper:** Python (FastAPI)
- **Hosting:** Google Cloud Run, Supabase, Cloudflare (kept close to free)
- **Everything lives in one repository** (pnpm + Turborepo), with automated tests and checks on every change in GitHub Actions.

## How it's being built

This project is built almost entirely by AI. Claude Code agents write the specs, plans, tests and code, following GitHub Spec Kit. Each feature goes through the same steps:

1. Write down what it should do and get it approved.
2. Plan it, and break it into small tasks.
3. Write the tests first, then the code that passes them.
4. Review it, and open a pull request.

Nandan reviews and approves at each step, tests the result on a staging version, and decides what goes live.

More detail: [`docs/product/`](docs/product/) (what the app does and its rules), [`docs/adr/`](docs/adr/) (technical decisions), [`docs/engineering/git-workflow.md`](docs/engineering/git-workflow.md) (branches and releases).

## Running it locally

You need Go, Node.js with pnpm, Python with uv, and Docker.

```bash
git clone https://github.com/NandanMaheta/Swastik.git && cd Swastik
make setup && make db-up && cp .env.example .env && make migrate
make api                               # backend on http://localhost:8080
pnpm --filter @swastik/admin dev       # admin panel on http://localhost:5173
pnpm --filter @swastik/client start    # mobile app (press w to open in the browser)
```

---

Built by Nandan Maheta. The code is almost entirely AI-generated (Claude Code).
