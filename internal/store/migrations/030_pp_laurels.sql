-- How many weekly checkpoints a member has placed at this season: the
-- laurels the choir's monday names give.
alter table pp_member add column laurels bigint not null default 0;
