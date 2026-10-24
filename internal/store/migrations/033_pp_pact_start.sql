-- Whether a member's notice has started its work, and their withdrawals:
-- the day of the last as a day number times 8 plus how many that day.
alter table pp_member add column pact_started boolean not null default false;
alter table pp_member add column withdraws bigint not null default 0;
