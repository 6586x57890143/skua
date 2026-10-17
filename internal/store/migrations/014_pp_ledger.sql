-- /pp's ledger: every change to a purse, in order per server. Each row's
-- hash covers the row and the hash before it, so rewriting any row changes
-- every head published after it. A member's rows sum to their purse less
-- the day still open (pp_member.day_got). reason is open (a purse as it
-- stood when the ledger began), labour (one closed day of earning, inputs
-- holding what it was earned from) or class (a class change paid for).
create table pp_ledger (
	guild_id bigint   not null,
	seq      bigint   not null,
	user_id  bigint   not null,
	day      bigint   not null,
	reason   text     not null,
	delta    bigint   not null,
	inputs   bigint[] not null default '{}',
	hash     bytea    not null,
	primary key (guild_id, seq)
);

-- The open day's earning, the lowest and highest buff it was earned under,
-- and how many voice minutes and paying messages it came in.
alter table pp_member
	add column day_got     bigint not null default 0,
	add column buff_lo     bigint not null default 0,
	add column buff_hi     bigint not null default 0,
	add column day_minutes bigint not null default 0,
	add column day_paid    bigint not null default 0;

-- Where /pp posts the ledger's daily head and each season's seed, 0 for
-- nowhere.
alter table pp_guild add column log_channel bigint not null default 0;

-- Each season's random seed, committed before it starts: sha256(secret) is
-- posted first, the drand beacon at round mixes in what nobody knew then,
-- and the secret is posted once the season is over. randomness is null
-- until drawn and empty if drand never answered in time.
create table pp_season (
	season     bigint  primary key,
	secret     bytea   not null,
	round      bigint  not null,
	randomness bytea,
	revealed   boolean not null default false
);
