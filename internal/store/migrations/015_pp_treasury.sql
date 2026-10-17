-- /pp's treasury, per server: what it holds, the levy taken today and not
-- yet in the ledger, and the levy at the full rate today and on each day
-- before (up to a week), which says when the treasury is full. Its ledger
-- rows are user 0's.
alter table pp_guild
	add column treasury  bigint   not null default 0,
	add column levy_open bigint   not null default 0,
	add column levy_full bigint   not null default 0,
	add column levy_days bigint[] not null default '{}';

-- What the member's open day paid the treasury.
alter table pp_member add column day_levy bigint not null default 0;
