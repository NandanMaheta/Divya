# Swastik

Swastik is a small internal app that makes IPO investing in the Indian stock market easier to manage for a group of people.

New IPOs open almost every week, and a lot of work goes into each one. Someone has to decide which IPOs are worth applying for, tell everyone, remind them to apply before the deadline, check who got shares, work out each person's share of the profit, and keep track of who has paid whom. Usually this happens over WhatsApp and spreadsheets. Swastik puts all of it in one place and automates most of it.

## What it does

- **Apply or skip:** for every new IPO, the admin marks it **Apply** or **Do not apply**, and everyone sees that straight away.
- **Reminders:** clients get an "apply" task for each of their family accounts, with reminders until they mark it done.
- **Allotment and listing:** the admin records who got shares and the listing price, and the app works out the profit share for each person.
- **Payments:** clients see what they owe or will receive, mark it paid, and the admin confirms.
- **Reports:** earnings by month, quarter or financial year, for the admin and for each client.

There are two parts:

- a **web admin panel** for the person running it;
- a **mobile app** for clients (Android, plus a web version for iPhone).

It's private and invite-only. It doesn't place IPO applications or move money; it only helps people organise and keep track.

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

Built by Nandan Maheta, with the code written mostly by AI (Claude Code).
