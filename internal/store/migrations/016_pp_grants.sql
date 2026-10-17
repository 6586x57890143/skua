-- /pp's grants. The treasury's first levy day, after which grants open,
-- and what it granted in the current week; what each member was granted
-- in grant_season; and grants over the charter's second admin line,
-- waiting for one in the ledger channel until paid or refused.
alter table pp_guild
	add column levy_since     bigint not null default 0,
	add column grant_week     bigint not null default 0,
	add column grant_week_sum bigint not null default 0;

alter table pp_member
	add column granted      bigint not null default 0,
	add column grant_season bigint not null default 0;

create table pp_grant (
	id        bigserial primary key,
	guild_id  bigint not null,
	from_id   bigint not null,
	to_id     bigint not null,
	amount    bigint not null,
	reason    text   not null,
	state     text   not null default 'waiting',
	second_id bigint not null default 0
);
