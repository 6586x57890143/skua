-- Whether a member's notice has started its work, when it was posted (unix
-- milliseconds, its key's own), and the times they took down their own
-- notice for the post back: the day number times 8 plus how many that day.
alter table pp_member add column pact_started boolean not null default false;
alter table pp_member add column withdraws bigint not null default 0;
alter table pp_member add column pact_at bigint not null default 0;
