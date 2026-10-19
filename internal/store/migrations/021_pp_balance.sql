-- The balance /pp's day runs under, as JSON, and that day. A deploy that
-- ships a new balance mid-day keeps running this one until the day turns.
create table pp_balance (
	id   int    primary key default 1 check (id = 1),
	day  bigint not null,
	body jsonb  not null
);
