-- A member's chirograph or round robin, shared with its other signers and
-- saved whole on each of their rows, and what signing in company has
-- taught them: chirographs, signatures, full round robins and the day of
-- their last joinder.
alter table pp_member add column pact text not null default '';
alter table pp_member add column signs text not null default '';
