# plunge-only recipes (plunge#44). Upstream builds with the Makefile; this file
# is fork-owned and only covers the docs site in site/ for now.

default:
    @just --list

# Install the site's dependencies from its lock file.
site-install:
    cd site && npm ci

# Build the docs site (Astro + Starlight) into site/dist/, as
# site/workflows/pages.yml does. The build also runs the room, link and
# contrast checks, so a broken link or a token below WCAG AA fails it.
site: site-install
    cd site && npm run build

# Serve the docs site locally with live reload at
# http://127.0.0.1:<port>/plunge/. `just site-serve 4322` picks another port,
# which is what you want when another checkout or worktree is already serving.
site-serve port="4321": site-install
    cd site && npm run dev -- --host 127.0.0.1 --port {{port}}

# Build, then serve site/dist/ exactly as it will be published, at
# http://127.0.0.1:<port>/plunge/.
site-preview port="4321": site
    cd site && npm run preview -- --host 127.0.0.1 --port {{port}}

# The site's prose lint (the same rules as choudoufu's site).
site-lint:
    cd site && npm run lint:prose
