# Changelog

## [0.4.1](https://github.com/TitusKirch/scaffold/compare/v0.4.0...v0.4.1) (2026-08-30)


### Bug Fixes

* **pnpm:** drop the two settings pnpm never read ([e43f838](https://github.com/TitusKirch/scaffold/commit/e43f838cd86967b5a5d5933d625244b6bea3e3d4)), closes [#52](https://github.com/TitusKirch/scaffold/issues/52)

## [0.4.0](https://github.com/TitusKirch/scaffold/compare/v0.3.0...v0.4.0) (2026-08-30)


### Features

* **queue:** cut the queue branch on the first worker PR ([#44](https://github.com/TitusKirch/scaffold/issues/44)) ([aa27382](https://github.com/TitusKirch/scaffold/commit/aa27382324b1d062691f7fa5889475e804b869de))
* **scripts:** migrate the meta scripts to TypeScript ([8e9e87d](https://github.com/TitusKirch/scaffold/commit/8e9e87d63410946f61377ccc3430a3415bc576de)), closes [#49](https://github.com/TitusKirch/scaffold/issues/49)


### Bug Fixes

* **ci:** let the queue PR body wrap itself ([c212fd6](https://github.com/TitusKirch/scaffold/commit/c212fd63d183e765617b80bbba69045895b40640))

## [0.3.0](https://github.com/TitusKirch/scaffold/compare/v0.2.0...v0.3.0) (2026-08-03)


### Features

* check the two agent policy files ban the same commands ([94b7668](https://github.com/TitusKirch/scaffold/commit/94b7668cfbb42a6fca8507533a08338225a4c5d8)), closes [#38](https://github.com/TitusKirch/scaffold/issues/38)


### Bug Fixes

* align the command bans across the two agent policy files ([98cc059](https://github.com/TitusKirch/scaffold/commit/98cc0599533e8a5fa260e88603c8ee8a329f85b9)), closes [#38](https://github.com/TitusKirch/scaffold/issues/38)
* **ci:** read the Queue App PEM from this owner's own -ci mirror ([42cc46c](https://github.com/TitusKirch/scaffold/commit/42cc46c0f21805b010fdb3389c897346be18dd15))

## [0.2.0](https://github.com/TitusKirch/scaffold/compare/v0.1.2...v0.2.0) (2026-07-26)


### Features

* add the Codex execution policy alongside the Claude one ([8733213](https://github.com/TitusKirch/scaffold/commit/8733213bb6260d8e29574ccf837b0253a5e21a12))
* document the main-only switch instead of vendoring it ([4c6d96c](https://github.com/TitusKirch/scaffold/commit/4c6d96c02e10204d796709002db3a39d9cb08da4))
* make the dev integration branch the default ([3fc7b75](https://github.com/TitusKirch/scaffold/commit/3fc7b758be2b083823271d330d0020fa11edccae))
* rework issue and PR templates and route low-friction reports to Discord ([230847a](https://github.com/TitusKirch/scaffold/commit/230847a3364b7b42bac38383dd05a9d281675388))
* rework the Claude permission policy ([ae91b53](https://github.com/TitusKirch/scaffold/commit/ae91b53518c68db6f53456e46c952a044174c075))
* ship the dev integration branch as an opt-in template variant ([0c913cb](https://github.com/TitusKirch/scaffold/commit/0c913cbf3541575f24abec074d76e51e752d8c8e))


### Bug Fixes

* align issue-template labels with the label catalog ([60f979c](https://github.com/TitusKirch/scaffold/commit/60f979c4e4c137b3ed1d91493fad137a86f10743))
* **ci:** run CI on pull requests into dev ([a885c8b](https://github.com/TitusKirch/scaffold/commit/a885c8ba0d02d742d0912e1eebd26fe952243cb5))

## [0.1.2](https://github.com/TitusKirch/scaffold/compare/v0.1.1...v0.1.2) (2026-07-20)


### Bug Fixes

* align dependabot labels to the stack: convention ([d867bc7](https://github.com/TitusKirch/scaffold/commit/d867bc7beef715f838366490a76b3cc45a70e223))

## [0.1.1](https://github.com/TitusKirch/scaffold/compare/v0.1.0...v0.1.1) (2026-05-27)


### Bug Fixes

* **release:** bump package.json via release-type node ([6468b9c](https://github.com/TitusKirch/scaffold/commit/6468b9c3579e360cce0e6a4df0a4b72b1e97aada))

## 0.1.0 (2026-05-25)


### Features

* **ci:** run release-please under the kirchDev Release App ([#3](https://github.com/TitusKirch/scaffold/issues/3)) ([7c348b9](https://github.com/TitusKirch/scaffold/commit/7c348b988d6076cc3282eac58b7dde13699b1c77))
